package service

import "github.com/pkg/errors"

var ErrUserExisted = errors.New("用户已存在")
var ErrAccountOrPassword = errors.New("账号或密码错误")
var ErrUnauthorized = errors.New("未登录或token无效")

var ErrKnowledgeBaseNotFound = errors.New("知识库不存在")
var ErrDocumentNotFound = errors.New("文档不存在")
var ErrMissingFile = errors.New("文件为空")
var ErrUnsupportedFileType = errors.New("文件类型不支持")
var ErrQuestionEmpty = errors.New("问题不能为空")
var ErrChatLogNotFound = errors.New("问答日志不存在")
var ErrConversationNotFound = errors.New("会话不存在或无权限访问")
var ErrInvalidFeedback = errors.New("反馈参数错误")
var ErrDocumentIndexing = errors.New("文档正在索引中")
var ErrLowConfidence = errors.New("检索置信度不足")
