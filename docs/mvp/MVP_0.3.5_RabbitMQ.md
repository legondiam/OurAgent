# MVP 0.3.5 RabbitMQ异步任务实现说明

## 目标

MVP 0.3.5引入RabbitMQ，把当前文档上传后的本地goroutine索引改为消息队列异步任务，并为文档删除增加补偿清理能力。

核心目标有两个：

- 文档索引任务不再依赖当前API进程内的goroutine，避免多用户批量上传时任务堆在单个请求进程里
- 文档删除先进入不可查询状态，再通过RabbitMQ异步清理Qdrant、Bluge、MySQL分块和MinIO对象，失败后由消息重试兜底

本版本不做复杂任务中心，不新增`cleanup_failed`文档状态。文档表只保存业务可见状态，失败重试交给RabbitMQ和消费者日志处理。

## 当前问题

当前上传流程是：

```text
上传文件
-> 写入MinIO
-> 创建documents记录，status=pending
-> 启动go func执行IndexAsync
-> API返回
```

这个方案在单机开发阶段够用，但有几个问题：

- API进程重启后，已经启动但未完成的goroutine会丢失
- 多用户同时上传大文件时，索引任务数量不可控
- 任务没有统一重试、限流、死信和积压观察能力
- 删除文档时涉及Qdrant、Bluge、MySQL、MinIO多个系统，任何一步失败都需要补偿清理

MVP 0.3.5用RabbitMQ把这些后台工作从请求生命周期里拆出来。

## 文档状态设计

继续使用`documents.status`作为RAG查询的权威状态。

```text
pending      已创建文档记录，等待索引
processing   正在索引
completed    索引完成，可以进入RAG
failed       索引失败
deleting     已逻辑删除，不再进入RAG，等待物理清理
```

查询侧规则：

```text
只有status=completed的文档允许进入RAG上下文
```

因此删除时只要先把文档状态改成`deleting`，即使Qdrant或Bluge里还有残留索引，回查MySQL文档状态时也会被过滤，不会污染问答结果。

## RabbitMQ队列设计

MVP阶段使用一个direct exchange和两个业务队列。

```text
exchange: ouragent.tasks

routing_key: document.index
queue: ouragent.document.index

routing_key: document.delete.cleanup
queue: ouragent.document.delete.cleanup
```

重试建议使用TTL重试队列加死信交换机。删除清理的首次投递也先进入`ouragent.document.delete.cleanup.retry`，等待TTL到期后再自动进入真正执行删除的主队列：

```text
ouragent.document.index.retry
ouragent.document.delete.cleanup.retry
ouragent.document.index.dlq
ouragent.document.delete.cleanup.dlq
```

MVP可以先配置一档固定延迟，例如30秒；后续再扩展成多级延迟或指数退避。

## 消息结构

索引消息：

```json
{
  "event_id": "uuid",
  "type": "document.index",
  "document_id": 1,
  "user_id": 1,
  "knowledge_base_id": 1,
  "attempt": 0,
  "created_at": "2026-06-03T12:00:00+08:00"
}
```

删除清理消息：

```json
{
  "event_id": "uuid",
  "type": "document.delete.cleanup",
  "document_id": 1,
  "user_id": 1,
  "knowledge_base_id": 1,
  "object_key": "users/1/knowledge-bases/1/xxx.pdf",
  "attempt": 0,
  "created_at": "2026-06-03T12:00:00+08:00"
}
```

`event_id`用于日志追踪和排查重复投递。MVP不强制做消息去重表，消费者逻辑必须保证幂等。

## 上传索引流程

API流程：

```text
POST /knowledge-bases/:id/documents
-> 校验知识库归属
-> 校验文件类型
-> 上传原始文件到MinIO
-> 创建documents记录，status=pending
-> 发布document.index消息
-> 返回document_id和pending状态
```

如果消息发布失败：

- 不启动goroutine兜底，避免双路径并存
- 将文档状态更新为`failed`
- 记录错误信息，例如“索引任务投递失败”
- API返回错误，用户可通过重建索引接口重新投递任务

这样比“文档一直pending但没有消费者知道”更容易排查。

## 索引消费者流程

消费者处理`document.index`消息：

```text
收到document.index消息
-> 查询documents记录
-> 文档不存在：ack，结束
-> status=deleting：ack，结束
-> status不是pending：ack，结束
-> 将pending更新为processing
-> 执行原Indexer.Index核心流程
-> 成功：status=completed，ack
-> 失败：按重试策略nack或投递到retry队列
-> 超过最大重试次数：status=failed，ack或进入DLQ
```

需要把状态更新做成条件更新：

```sql
UPDATE documents
SET status = 'processing'
WHERE id = ? AND user_id = ? AND status = 'pending'
```

只有更新成功的消费者才能继续索引。这样可以避免重复消息、并发消费或用户连续点击重建索引导致同一个文档被多个worker同时处理。

## 删除补偿流程

删除API流程：

```text
DELETE /documents/:id
-> 查询并校验文档归属
-> 如果status=pending或processing，拒绝删除
-> 将status更新为deleting
-> 发布document.delete.cleanup.retry消息
-> 等待TTL到期后自动转入document.delete.cleanup队列
-> 返回成功或deleting状态
```

这里的关键是先切`deleting`，再发布延迟清理消息。只要状态切换成功，该文档就不会进入RAG，物理清理可以等外部存储或索引服务恢复后再执行。

如果消息发布失败：

- API返回错误
- 文档保持`deleting`
- 用户再次调用DELETE时可以重新投递清理消息
- 后续也可以通过定时扫描`status=deleting`的文档补发清理消息

MVP阶段不需要`cleanup_failed`状态。`deleting`本身就表示“已不可查询，物理清理待完成”。

## 删除消费者流程

消费者处理`document.delete.cleanup`消息：

```text
收到document.delete.cleanup消息
-> 查询documents记录
-> 文档不存在：ack，结束
-> status不是deleting：ack，结束
-> 删除Qdrant向量点
-> 删除Bluge关键词索引
-> 删除MySQL父子chunk
-> 删除MinIO原始对象
-> 删除documents记录
-> ack
```

任何一步失败：

```text
不删除documents记录
消息进入retry队列
重试后继续按同样流程清理
超过最大重试次数进入DLQ
```

删除消费者必须幂等：

- Qdrant按`user_id + knowledge_base_id + document_id`删除，404视为成功
- Bluge按`user_id + document_id`删除，找不到索引视为成功
- MySQL删除分块按`document_id + user_id`执行，删除0行视为成功
- MinIO删除对象时对象不存在视为成功
- document记录已不存在时直接ack

## 重建索引流程

重建索引仍走索引队列：

```text
POST /documents/:id/reindex
-> 拒绝pending/processing/deleting文档
-> 将status更新为pending
-> 发布document.index消息
-> 返回pending
```

真正清理旧索引和旧分块的逻辑仍放在Indexer里：

```text
processing
-> 删除旧Qdrant向量
-> 删除旧Bluge索引
-> 删除旧MySQL分块
-> 重新解析、切片、embedding、写入索引
-> completed
```

后续如果要进一步提升一致性，可以再做索引版本号，但MVP 0.3.5先保持当前重建策略。

## 配置项

新增配置建议：

```yaml
rabbitmq:
  enabled: true
  url: "amqp://guest:guest@localhost:5672/"
  exchange: "ouragent.tasks"
  index_queue: "ouragent.document.index"
  delete_queue: "ouragent.document.delete.cleanup"
  retry_delay_seconds: 30
  max_retries: 5
  index_workers: 2
  delete_workers: 2
  prefetch_count: 1
```

`prefetch_count`建议先设为1，避免单个消费者一次拿太多大文档索引任务。

## 代码改动范围

建议新增包：

```text
internal/queue
  rabbitmq.go             RabbitMQ连接、exchange/queue声明、发布和消费封装

internal/tasks
  messages.go             文档索引和删除清理消息结构
  producer.go             任务发布器
  index_consumer.go       文档索引消费者
  delete_consumer.go      文档删除清理消费者
```

需要调整的已有模块：

```text
internal/config
  增加RabbitMQ配置

cmd/server
  初始化RabbitMQ连接、任务发布器和消费者

internal/service/document_service.go
  Upload/Reindex不再调用IndexAsync，改为发布document.index消息
  Delete改为切deleting状态后发布document.delete.cleanup.retry延迟消息

internal/document/indexer.go
  保留Index(ctx, documentID)
  移除IndexAsync，消费者直接调用Index(ctx, documentID)

internal/rag/retrieved_chunk_loader.go
  保持只允许completed文档进入RAG上下文
```

## 可靠性边界

MVP 0.3.5保证：

- 上传接口不直接执行索引重活
- 多用户上传时通过队列削峰
- 文档删除先逻辑屏蔽，避免残留索引污染RAG
- 索引和删除清理支持RabbitMQ重试
- 消费者幂等，重复消息不会破坏数据

MVP 0.3.5暂不保证：

- 数据库状态更新和RabbitMQ发布的强事务一致性
- 复杂任务进度百分比
- 多级指数退避
- 管理后台查看DLQ和手动重放
- 索引版本切换

后续如果要解决“DB更新成功但消息发布失败”的极端情况，可以引入outbox表或定时扫描补偿：

```text
扫描status=pending但长时间未processing的文档，补发document.index消息
扫描status=deleting但记录仍存在的文档，补发document.delete.cleanup消息
```

这可以作为MVP 0.3.6或0.4的增强项。
