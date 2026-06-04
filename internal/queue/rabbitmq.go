package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"OurAgent/internal/config"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	DocumentIndexRoutingKey       = "document.index"
	DocumentIndexRetryRoutingKey  = "document.index.retry"
	DocumentIndexDLQRoutingKey    = "document.index.dlq"
	DocumentDeleteRoutingKey      = "document.delete.cleanup"
	DocumentDeleteRetryRoutingKey = "document.delete.cleanup.retry"
	DocumentDeleteDLQRoutingKey   = "document.delete.cleanup.dlq"
	contentTypeJSON               = "application/json"
)

type Client struct {
	conn     *amqp.Connection
	exchange string
}

type Delivery struct {
	Body []byte
	ack  func(bool) error
	nack func(bool, bool) error
}

func NewRabbitMQClient(cfg config.RabbitMQConfig) (*Client, error) {
	conn, err := amqp.Dial(cfg.URL)
	if err != nil {
		return nil, err
	}
	client := &Client{conn: conn, exchange: cfg.Exchange}
	if err := client.declareTopology(cfg); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) PublishJSON(ctx context.Context, routingKey string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.PublishRaw(ctx, routingKey, body)
}

func (c *Client) PublishRaw(ctx context.Context, routingKey string, body []byte) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	return ch.PublishWithContext(ctx, c.exchange, routingKey, true, false, amqp.Publishing{
		ContentType:  contentTypeJSON,
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Body:         body,
	})
}

func (c *Client) Consume(ctx context.Context, queueName string, prefetch int, handler func(context.Context, Delivery)) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	if prefetch <= 0 {
		prefetch = 1
	}
	if err := ch.Qos(prefetch, 0, false); err != nil {
		_ = ch.Close()
		return err
	}
	deliveries, err := ch.Consume(queueName, "", false, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		return err
	}
	go func() {
		defer ch.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-deliveries:
				if !ok {
					return
				}
				handler(ctx, Delivery{
					Body: msg.Body,
					ack:  msg.Ack,
					nack: msg.Nack,
				})
			}
		}
	}()
	return nil
}

func (d Delivery) Ack() error {
	return d.ack(false)
}

func (d Delivery) Nack(requeue bool) error {
	return d.nack(false, requeue)
}

func (c *Client) declareTopology(cfg config.RabbitMQConfig) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if err := ch.ExchangeDeclare(cfg.Exchange, "direct", true, false, false, false, nil); err != nil {
		return err
	}
	if err := c.declareTaskQueues(ch, cfg.Exchange, cfg.IndexQueue, DocumentIndexRoutingKey, DocumentIndexRetryRoutingKey, DocumentIndexDLQRoutingKey, cfg.RetryDelaySeconds); err != nil {
		return err
	}
	if err := c.declareTaskQueues(ch, cfg.Exchange, cfg.DeleteQueue, DocumentDeleteRoutingKey, DocumentDeleteRetryRoutingKey, DocumentDeleteDLQRoutingKey, cfg.RetryDelaySeconds); err != nil {
		return err
	}
	return nil
}

func (c *Client) declareTaskQueues(ch *amqp.Channel, exchange, queueName, routingKey, retryRoutingKey, dlqRoutingKey string, retryDelaySeconds int) error {
	retryQueue := queueName + ".retry"
	dlqQueue := queueName + ".dlq"
	if retryDelaySeconds <= 0 {
		retryDelaySeconds = 30
	}
	retryDelayMS := int32(retryDelaySeconds * 1000)
	if err := declareAndBind(ch, queueName, exchange, routingKey, nil); err != nil {
		return err
	}
	if err := declareAndBind(ch, retryQueue, exchange, retryRoutingKey, amqp.Table{
		"x-message-ttl":             retryDelayMS,
		"x-dead-letter-exchange":    exchange,
		"x-dead-letter-routing-key": routingKey,
	}); err != nil {
		return err
	}
	if err := declareAndBind(ch, dlqQueue, exchange, dlqRoutingKey, nil); err != nil {
		return err
	}
	return nil
}

func declareAndBind(ch *amqp.Channel, queueName, exchange, routingKey string, args amqp.Table) error {
	if strings.TrimSpace(queueName) == "" {
		return fmt.Errorf("queue name is empty")
	}
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, args); err != nil {
		return err
	}
	return ch.QueueBind(queueName, routingKey, exchange, false, nil)
}
