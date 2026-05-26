package service

import (
	"OurAgent/internal/model"
	"OurAgent/internal/repository"

	pkgerrors "github.com/pkg/errors"
)

type KnowledgeBaseService struct {
	kbs *repository.KnowledgeBaseRepository
}

type CreateKnowledgeBaseInput struct {
	UserID      uint64
	Name        string
	Description string
}

func NewKnowledgeBaseService(kbs *repository.KnowledgeBaseRepository) *KnowledgeBaseService {
	return &KnowledgeBaseService{kbs: kbs}
}

// Create 创建知识库
func (s *KnowledgeBaseService) Create(input CreateKnowledgeBaseInput) (*model.KnowledgeBase, error) {
	kb := &model.KnowledgeBase{
		UserID:      input.UserID,
		Name:        input.Name,
		Description: input.Description,
	}
	if err := s.kbs.Create(kb); err != nil {
		return nil, pkgerrors.WithMessage(err, "创建知识库失败")
	}
	return kb, nil
}

// List 查询用户知识库列表
func (s *KnowledgeBaseService) List(userID uint64) ([]model.KnowledgeBase, error) {
	items, err := s.kbs.ListByUserID(userID)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询知识库失败")
	}
	return items, nil
}

// Delete 删除用户知识库
func (s *KnowledgeBaseService) Delete(userID, id uint64) error {
	rows, err := s.kbs.DeleteByIDAndUserID(id, userID)
	if err != nil {
		return pkgerrors.WithMessage(err, "删除知识库失败")
	}
	if rows == 0 {
		return pkgerrors.WithStack(ErrKnowledgeBaseNotFound)
	}
	return nil
}

// Exists 判断用户知识库是否存在
func (s *KnowledgeBaseService) Exists(userID, id uint64) (bool, error) {
	exists, err := s.kbs.ExistsByIDAndUserID(id, userID)
	if err != nil {
		return false, pkgerrors.WithMessage(err, "查询知识库失败")
	}
	return exists, nil
}
