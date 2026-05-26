package service

import "github.com/pkg/errors"

var ErrUserExisted = errors.New("用户已存在")
var ErrAccountOrPassword = errors.New("账号或密码错误")
var ErrUnauthorized = errors.New("未登录或 token 无效")

var ErrKnowledgeBaseNotFound = errors.New("知识库不存在")
var ErrDocumentNotFound = errors.New("文档不存在")
var ErrMissingFile = errors.New("文件为空")
var ErrUnsupportedFileType = errors.New("文件类型不支持")
var ErrQuestionEmpty = errors.New("问题不能为空")
