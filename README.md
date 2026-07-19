# featcache — AI 特征向量的零拷贝运行时缓存

> 一次加载，多进程零拷贝共享，热切换

[![Go Report Card](https://goreportcard.com/badge/github.com/hengli-coder/featcache)](https://goreportcard.com/report/github.com/hengli-coder/featcache)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

---

## 概述

**featcache** 是一个面向 AI 推理场景的零拷贝运行时数据缓存。它解决的核心问题是：

> AI 推理进程启动时，大量静态数据（Embedding、Tokenizer、Feature Dictionary 等）的重复加载导致启动慢、资源浪费。

### 工作原理

```
┌──────────────────────────┐   一次加载    ┌──────────────────┐
│  Loader 守护进程          │ ───────────► │  共享内存段       │
│  • 从数据源加载工件         │              │  [Header]        │
│  • 写入共享内存，构建索引    │              │  [Hash Index]    │
│  • 监听控制面 UDS          │              │  [Data Region]   │
│  • 支持热切换              │              └──────┬───────────┘
└──────────────────────────┘                     │ mmap
                                                 ▼
                                    ┌──────────────────────────┐
                                    │  推理进程 1  推理进程 2  │
                                    │  推理进程 3  ... 进程 N  │
                                    │  直接读共享内存，零拷贝   │
                                    │  无锁，无 syscall        │
                                    └──────────────────────────┘
```

### 适用场景

| 场景 | 数据示例 | 典型大小 |
|------|---------|---------|
| 推荐系统 | User/Item Embedding 表 | 10GB~100GB |
| LLM 推理 | Tokenizer 词汇表、BPE 编码 | 1GB~10GB |
| 多模态模型 | 图像/文本特征字典 | 5GB~50GB |
| 广告 CTR 预估 | 稀疏特征字典、Lookup 表 | 10GB~30GB |
| RAG 系统 | 文档 Embedding 库 | 10GB~100GB |
| 搜索引擎 | ANN 索引、倒排表 | 5GB~50GB |

### 核心特性

- **零拷贝读取** — 客户端直接读共享内存，查询延迟 < 100ns
- **一次加载，多进程共享** — Loader 加载一次，N 个进程共享同一份数据
- **启动即用** — 推理进程启动仅需 mmap，< 100ms，与数据量无关
- **热切换**（二期）— 运行时替换数据，不中断服务
- **紧凑存储** — 无内部碎片，10GB+ 数据量节省 30%+ 空间
- **纯 Go** — 仅依赖 `golang.org/x/sys`

### 与其他方案的对比

| 方案 | 零拷贝多进程共享 | 查询延迟 | 热更新 | 10GB+ 优化 | 外部依赖 |
|------|----------------|---------|--------|-----------|---------|
| **featcache** | ✅ | < 100ns | ✅ (二期) | ✅ | 无 |
| Redis | ❌ 网络通信 | ~100μs | ✅ | ❌ | 无 |
| FAISS | ⚠️ mmap 共享 | < 100ns | ❌ | ✅ | C++ |
| Plasma (已废弃) | ✅ | < 100ns | ❌ | ❌ | C++ |
| Safetensors | ❌ 各自 mmap | < 100ns | ❌ | ✅ | Python/C++ |

---

## 快速开始

```bash
# 构建
go build ./cmd/featload

# 启动加载器（加载 10GB embedding 到共享内存）
featload -name my-embeddings -size 10737418240 -source /data/embeddings.bin

# 推理进程中使用 Reader
```

```go
import "github.com/hengli-coder/featcache"

// 初始化 Reader（< 100ms，无论数据多大）
reader, err := featcache.NewReader("\x00featcache-my-embeddings")

// 查询特征向量（纳秒级）
embedding, ok := reader.Get([]byte("user_embedding_123"))
```

---

## 架构

详见 [DESIGN.md](docs/DESIGN.md)

### 数据流

```
Loader 启动 → 从数据源加载 → 写入共享内存 → 就绪
                                                 ↓
推理进程启动 → UDS 获取元数据 → mmap 共享内存 → 直接查询
                                                 ↓
               所有 GET 操作走共享内存，不走 UDS
```

### 控制面与数据面分离

- **控制面**：Unix Domain Socket，仅用于初始化和版本通知
- **数据面**：共享内存，所有数据读取直接在这里完成

---

## 项目结构

```
featcache/
├── go.mod / go.sum
├── README.md
├── CLAUDE.md
├── .golangci.yml
├── .gitignore
├── cmd/
│   └── featload/           # Loader 守护进程入口
│       └── main.go
└── pkg/
    └── featcache/          # 核心库
        ├── types.go        # 公共类型、常量
        ├── hash.go         # 哈希函数
        ├── segment.go      # 共享内存段管理（平台无关接口）
        ├── segment_linux.go # Linux mmap 实现
        ├── segment_other.go # 非 Linux 桩
        ├── hashtable.go    # 开放寻址哈希表
        ├── loader.go       # 批量加载器（写入者）
        ├── reader.go       # 零拷贝读取者
        ├── datasource.go   # 数据源接口
        ├── protocol.go     # UDS 控制面协议
        ├── featcache_test.go # 测试
        └── example_test.go   # 示例
```

---

## 配置参数

### featload（Loader 守护进程）

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-name` | `featcache` | 共享内存段名称 |
| `-size` | `2GB` | 共享内存大小 |
| `-uds` | `\x00featcache` | UDS 地址（抽象命名空间） |
| `-source` | 必填 | 数据源路径 |

---

## 开发

```bash
# 运行测试
go test ./pkg/featcache/ -v -count=1

# 带竞态检测
go test ./pkg/featcache/ -v -race -count=1

# 基准测试
go test ./pkg/featcache/ -bench=. -benchmem -count=1

# 构建
go build ./cmd/featload
```

---

## License

MIT