# OurAgent v0.3.1 MVP实现说明

本文档记录v0.3.1父子切割的实现范围。v0.3已经完成Markdown标题层级切分和无结构文本overlap切分，v0.3.1在此基础上继续完成“子chunk检索、父chunk阅读”的索引和检索链路。

## 1. 版本目标

v0.3.1目标：

> 将文档切片升级为父子chunk结构。小的子chunk用于向量检索，命中后回查对应父chunk，将父chunk内容交给LLM阅读，从而兼顾向量检索的聚焦性和模型阅读上下文的完整性。

核心流程：

```text
索引阶段:
文档 -> parent_chunk -> child_chunk -> child向量入Qdrant

检索阶段:
用户问题 -> 命中child_chunk -> 回查parent_chunk -> parent内容进入Prompt
```

## 2. 实现范围

### 2.1 已实现

- 新增document_parent_chunks父切片表
- 新增document_child_chunks子切片表
- Markdown section作为父chunk
- 父chunk内部按chunk_size和chunk_overlap生成子chunk
- TXT/PDF先生成较大的父chunk，再在父chunk内部生成子chunk
- 只有子chunk进行Embedding
- 只有子chunk写入Qdrant
- Qdrant payload写入parent_chunk_id和chunk_type
- Retriever命中子chunk后回查父chunk
- ContextBuilder使用父chunk内容构建Prompt上下文
- sources返回命中的子chunk_id和parent_chunk_id
- retrieval_trace返回命中的子chunk_id和parent_chunk_id
- 同一个父chunk被多个子chunk命中时只进入一次上下文

### 2.2 暂不实现

- parent去重后的多child分数融合
- parent内容摘要压缩
- parent窗口扩展
- QueryRewrite
- Rerank
- 多路检索
- 独立父子chunk配置项

## 3. 数据模型

父chunk表：

```go
type DocumentParentChunk struct {
	ID              uint64
	DocumentID      uint64
	KnowledgeBaseID uint64
	UserID          uint64
	ChunkIndex      int
	SectionPath     string
	Content         string
	TokenCount      int
}
```

子chunk表：

```go
type DocumentChildChunk struct {
	ID              uint64
	ParentChunkID   uint64
	DocumentID      uint64
	KnowledgeBaseID uint64
	UserID          uint64
	ChunkIndex      int
	SectionPath     string
	Content         string
	TokenCount      int
	VectorID        string
}
```

职责划分：

```text
document_parent_chunks 用于LLM阅读，不写向量库
document_child_chunks 用于向量检索，写入Qdrant
parent_chunk_id 表示子chunk所属父chunk
```

## 4. 索引流程

父子切割后的索引流程：

```text
1. 解析文档
2. NormalizeText
3. SplitDocument生成ParentChunk
4. 保存document_parent_chunks
5. 遍历parent.Children
6. 对child生成Embedding
7. 保存document_child_chunks
8. 将child向量写入Qdrant
9. Qdrant payload记录parent_chunk_id
```

说明：

- 父chunk不写入Qdrant
- 子chunk写入Qdrant
- 文档chunk_count统计子chunk数量

## 5. 检索流程

父子检索流程：

```text
1. 用户问题向量化
2. Qdrant命中child_chunk
3. 回查document_child_chunks获取child_chunk
4. 根据child.parent_chunk_id回查document_parent_chunks
5. RetrievedChunk中Chunk表示parent
6. RetrievedChunk中MatchedChunk表示命中的child
7. ContextBuilder将parent内容写入Prompt
8. sources和trace记录child和parent关系
```

## 6. Prompt上下文

Prompt阅读的是父chunk内容。

来源格式仍然保留：

```text
[来源1: 文档名 / section_path / chunk N]
父chunk正文
```

如果多个child命中同一个parent：

```text
第一个命中的child让parent进入上下文
后续同parent命中只记录trace，过滤原因为父chunk已进入上下文
```

## 7. sources和trace

sources新增：

```json
{
  "parent_chunk_id": 100,
  "chunk_id": 120
}
```

含义：

```text
parent_chunk_id 表示实际给LLM阅读的父chunk
chunk_id 表示Qdrant命中的子chunk
```

retrieval_trace同样记录二者关系，便于排查命中的是哪个子chunk、阅读的是哪个父chunk。

## 8. 验收标准

- 上传Markdown后能生成父切片和子切片
- 上传TXT/PDF后能生成父切片和子切片
- 父切片和子切片分别写入两张表
- Qdrant只写入child向量
- 问答检索命中child后能回查parent
- Prompt上下文使用parent内容
- sources返回parent_chunk_id和chunk_id
- retrieval_trace返回parent_chunk_id和chunk_id
- 同一个parent被多个child命中时不会重复进入Prompt
- 文档删除和重建索引能清理父子chunk和向量

## 9. 注意事项

已有旧索引数据仍在旧document_chunks表中。当前分表实现使用document_parent_chunks和document_child_chunks，要体验父子切割效果，需要对已有文档执行重建索引。
