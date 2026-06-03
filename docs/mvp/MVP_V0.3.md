# OurAgent v0.3 MVP实现说明

本文档用于指导OurAgent第三版开发。v0.3的目标不是继续扩大Agent能力，而是在v0.2已经完成Eino RAGChain编排的基础上，优先优化文档索引阶段的chunk切分质量。

v0.3聚焦：

```text
Markdown标题层级切分
无结构文本overlap切分
```

暂缓实现：

```text
父子切割
```

## 1. 版本目标

v0.3目标：

> 将当前固定长度切分升级为结构感知切分。Markdown文档优先按标题层级形成语义边界，无明显结构的TXT和PDF文本继续使用固定长度加overlap切分。系统需要保留chunk所属章节路径，提升向量检索、Prompt上下文和引用来源的可读性。

当前问题：

- 固定长度切分可能打断Markdown章节语义
- chunk缺少标题路径，单独看正文时上下文不足
- sources只能返回文档名和chunk序号，无法定位到文档章节
- 后续父子切割需要先建立文档结构信息

v0.3完成后：

```text
Markdown:
标题层级解析 -> section_path -> section内切分 -> chunk入库

TXT/PDF:
纯文本规范化 -> 固定长度+overlap切分 -> chunk入库
```

## 2. v0.3范围

### 2.1 必须实现

- 抽象chunk切分结果结构
- Markdown按标题层级识别section
- Markdown chunk保存section_path
- Markdown section过长时在section内部按chunk_size+chunk_overlap继续切分
- TXT和PDF继续走固定长度+chunk_overlap切分
- document_chunks表增加section_path字段
- Qdrant payload写入section_path
- Retriever回查chunk后带出section_path
- ContextBuilder构建上下文时展示section_path
- sources返回section_path
- retrieval_trace记录section_path
- 文档重建索引时使用新的切分策略
- 保持v0.2问答链路、流式问答、文档删除和重建索引能力不被破坏

### 2.2 暂不实现

- 父子切割
- parent_chunk和child_chunk表结构
- 小chunk检索、大chunk阅读
- 多路chunk策略
- 语义相似度自动合并section
- OCR版PDF结构化解析
- Docling、Tika、Unstructured外部解析服务接入
- Rerank
- QueryRewrite

## 3. 核心概念

### 3.1 section_path

`section_path`表示chunk在原文档中的章节路径。

示例Markdown：

```md
# 支付模块
## 回调处理
### 验签失败原因

支付回调验签失败通常包括密钥不一致、参数缺失、签名算法错误。
```

对应chunk：

```text
section_path = 支付模块/回调处理/验签失败原因
content = 支付回调验签失败通常包括密钥不一致、参数缺失、签名算法错误。
```

用途：

- embedding时保留标题语义
- Prompt上下文中展示章节来源
- sources中返回更清晰的引用位置
- 为后续父子切割预留结构基础

### 3.2 结构化切分

结构化切分不是简单按字符数切，而是优先尊重文档原有语义边界。

Markdown文档优先按标题组织：

```text
标题路径
-> 标题下正文
-> section过长再按长度切分
```

无结构文档继续使用：

```text
chunk_size + chunk_overlap
```

## 4. 数据模型调整

### 4.1 document_chunks新增字段

新增：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| section_path | varchar/text | chunk所属Markdown标题路径 |

建议模型字段：

```go
SectionPath string `gorm:"size:1024" json:"section_path"`
```

说明：

- Markdown chunk必须尽量写入section_path
- TXT和PDF可以为空
- 后续如果PDF解析能识别标题，也可以复用该字段

## 5. 切分策略设计

### 5.1 Chunk结构

建议新增内部切分结果结构：

```go
type Chunk struct {
	Content     string
	SectionPath string
	TokenCount   int
}
```

索引流程不再只接收`[]string`，而是接收结构化chunk列表。

### 5.2 Markdown切分

Markdown处理流程：

```text
1. 按行读取Markdown
2. 识别#、##、###等标题
3. 维护当前标题栈
4. 将标题下正文聚合成section
5. 为section生成section_path
6. section不超过chunk_size时直接生成chunk
7. section超过chunk_size时在section内部按chunk_size+chunk_overlap切分
```

标题栈示例：

```text
# 支付模块
## 回调处理
### 验签失败原因
```

生成：

```text
支付模块/回调处理/验签失败原因
```

注意：

- 标题行本身可以参与embedding内容
- section正文为空时不生成chunk
- 文档开头没有标题的正文可以归入空section_path
- 不需要在v0.3处理Markdown表格和代码块的特殊语义，只要不破坏原文本即可

### 5.3 无结构文本切分

TXT和PDF解析后仍然走当前固定长度切分：

```text
SplitText(text, chunk_size, chunk_overlap)
```

要求：

- 保留overlap
- 保持现有chunk_size和chunk_overlap配置
- section_path为空
- 不破坏当前索引链路

## 6. 索引流程调整

当前流程：

```text
ParseFile
-> NormalizeText
-> SplitText
-> Embedding
-> document_chunks
-> Qdrant
```

v0.3调整为：

```text
ParseFile
-> NormalizeText
-> Chunker按文件类型切分
-> Embedding
-> document_chunks写入content、section_path、token_count
-> Qdrant写入向量和payload
```

Qdrant payload新增：

```json
{
  "section_path": "支付模块/回调处理/验签失败原因"
}
```

## 7. 检索和上下文调整

Retriever回查MySQL后，`RetrievedChunk`中的`Chunk`应包含`SectionPath`。

ContextBuilder构建上下文时，来源块建议从：

```text
[来源1: pay.md / chunk 3]
```

升级为：

```text
[来源1: pay.md / 支付模块/回调处理/验签失败原因 / chunk 3]
```

如果`section_path`为空，则保持原格式。

## 8. API响应调整

`sources`新增：

```json
{
  "section_path": "支付模块/回调处理/验签失败原因"
}
```

示例：

```json
{
  "document_id": 1,
  "document_name": "支付说明.md",
  "section_path": "支付模块/回调处理/验签失败原因",
  "chunk_id": 10,
  "chunk_index": 2,
  "score": 0.86,
  "content_preview": "支付回调验签失败通常包括..."
}
```

## 9. retrieval_trace调整

`TraceHit`新增：

```go
SectionPath string `json:"section_path"`
```

用于排查命中的chunk来自哪个章节。

## 10. 父子切割暂缓说明

父子切割是后续重要方向，但v0.3暂缓实现。

原因：

- 会牵动表结构和索引流程
- Qdrant只存child向量，Prompt要回查parent
- Retriever需要从child映射到parent
- ContextBuilder需要parent去重和token控制
- sources和trace需要同时表达命中child和阅读parent

v0.3先完成section_path和结构化切分，为后续父子切割打基础。

后续父子切割目标：

```text
小chunk用于向量检索
大chunk用于LLM阅读
```

## 11. 开发顺序

建议按以下顺序实现：

```text
1. 为document_chunks增加section_path字段
2. 调整DocumentChunk模型
3. 定义结构化Chunk结果
4. 实现Markdown标题层级解析
5. 实现Markdown section内二次切分
6. 保留TXT/PDF固定长度+overlap切分
7. 修改Indexer写入section_path
8. 修改Qdrant payload写入section_path
9. 修改Source和TraceHit结构
10. 修改ContextBuilder来源展示
11. 修改sources响应
12. 跑通上传Markdown、问答和重建索引链路
```

## 12. 验收标准

v0.3完成后需要满足：

- Markdown文档能按标题层级生成section_path
- Markdown超长section仍能按chunk_size和chunk_overlap切分
- TXT和PDF仍能按原固定长度+overlap切分
- document_chunks能保存section_path
- Qdrant payload包含section_path
- 问答sources返回section_path
- retrieval_trace返回section_path
- Prompt上下文来源块展示section_path
- 原v0.2普通问答和流式问答不被破坏
- 文档删除和重建索引不被破坏

## 13. 面试表达重点

v0.3可以重点讲：

- 为什么固定长度切分会破坏Markdown语义边界
- 为什么Markdown适合按标题层级切分
- section_path如何增强检索语义和引用来源
- 为什么无结构文本仍然需要overlap
- 为什么父子切割有价值但不在本版直接实现
- 如何通过结构化chunk为后续父子检索和Rerank打基础

