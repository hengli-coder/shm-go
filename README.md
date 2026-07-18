# shm-go — 基于 Linux 共享内存的高速缓存服务

## 概述

`shm-go` 是一个基于 Linux POSIX 共享内存 (`shm_open` + `mmap`) 的高性能缓存服务。采用**单写入者 + 多读取者**架构，通过 Unix Domain Socket 进行控制面通信，数据面则让客户端直接读取共享内存实现零拷贝。

专为**大量数据加载场景**设计，例如 AI 模型推理时需要加载巨量 embedding/特征数据的场景。多个进程共享同一份内存数据，避免重复加载和冗余内存占用。

---

## 架构

```
┌─────────────────┐     UDS (控制面)      ┌─────────────────┐
│  Server (shmd)  │ ◄──────────────────► │  Client 进程    │
│  • 管理共享内存   │    GET key → offset   │  • 读取共享内存   │
│  • 唯一写入者     │    SET key,val        │  • 零拷贝读数据   │
│  • 处理 UDS 请求  │                      │  • 纯读者(无锁)   │
│  • TTL 过期清理   │                      │                 │
└────────┬────────┘                      └────────┬────────┘
         │ MAP_SHARED                              │ MAP_SHARED
         ▼                                         ▼
   ┌─────────────────────────────────────────────────────┐
   │            /dev/shm/shm-go-cache (tmpfs)              │
   │  ┌────────┬──────────┬──────────────┬─────────────┐  │
   │  │ Header │ Slab元数据 │  Hash Table  │  Data Chunks │  │
   │  │ 64B    │  HEADER   │              │             │  │
   │  └────────┴──────────┴──────────────┴─────────────┘  │
   └─────────────────────────────────────────────────────┘
```

核心设计思路：

1. **shmd 守护进程**：唯一写入者，管理共享内存的创建、数据写入、淘汰策略
2. **客户端**：通过 UDS 查询 key 对应的共享内存 offset，然后直接 mmap 同一块共享内存读取数据——零系统调用、零拷贝
3. **无锁读**：客户端只读，不需要任何锁。哈希表的状态变更通过 `sync/atomic` 的 CAS/Store 实现线程安全的发布

---

## 共享内存布局

```
Offset              Size     Field
────────────────────────────────────────
0                   64B      Header（魔数/版本/总大小/哈希表容量）
64                  seg/4    Slab 数据区（分段式块分配器）
slab_end            对齐     Hash Table slots（每个slot 16B）
ht_end              对齐     Data Chunks（实际存储key+value）
```

### Header（偏移 0，64 字节）

```go
type Header struct {
    Magic   uint32    // 0x53484D47 = "SHMG"
    Version uint32    // 布局版本号
    Size    uint64    // 共享内存总大小
    HashCap uint32    // 哈希表容量（2 的幂）
    _       [48]byte  // 对齐到 64B cache line
}
```

### Hash Slot（16 字节）

```go
type HashSlot struct {
    HashHigh uint32   // hash 高32位（快速预过滤）
    Status   uint32   // 0=空, 1=占用, 2=逻辑删除(tombstone)
    Offset   uint32   // 数据在 region 内的偏移（相对 slab data 基址）
    VLen     uint32   // value 长度
}
```

### Slab 分配器

固定大小分块，消除外部碎片。每种 class 管理一种块大小：

| Class | 块大小 | 适用数据 |
|-------|--------|---------|
| 0     | 64 B   | 小 KV |
| 1     | 128 B  | |
| 2     | 256 B  | |
| 3     | 512 B  | |
| 4     | 1 KB   | |
| 5     | 2 KB   | |
| 6     | 4 KB   | |
| 7     | 8 KB   | |
| 8     | 16 KB  | |
| 9     | 32 KB  | 大 KV |

空闲链表用 `atomic.CompareAndSwapInt32` 实现无锁 pop/push（偏移量用 int32 而非指针，避免不同 mmap 地址空间不一致问题）。

---

## 网络协议

通过 Unix Domain Socket（抽象命名空间 `\0shm-go-cache`）通信，二进制 TLV 格式：

**请求头（8 字节）：**
```
[Op:1B][Flags:1B][KeyLen:2B BE][ValLen:2B BE][TTL:2B BE]
[KeyBytes:KeyLen]
[ValBytes:ValLen]  (仅 SET)
```

**响应头（12 字节）：**
```
[Status:1B][Flags:1B][Offset:4B BE][ValLen:4B BE][Gen:2B BE]
[ValBytes:ValLen]  (仅 inline fallback)
```

---

## 并发模型

| 角色 | 操作 | 同步方式 |
|------|------|---------|
| shmd server | SET/DELETE | 唯一写入者，CAS 抢 slot |
| shmd server | Free list | CAS on FreeHead |
| 客户端进程 | GET 查询 | atomic.Load 读 Status |
| 客户端进程 | 读数据 | 直接读共享内存（纯读取） |

---

## 项目结构

```
d:\go\shm-go\
├── go.mod / go.sum
├── README.md                  ← 就是这个文件
├── cmd/
│   └── shmd/
│       ├── main.go            # Linux 入口（build tag: linux）
│       └── main_other.go      # 非 Linux 桩代码
├── pkg/
│   └── shmcache/
│       ├── types.go           # 公共类型、常量
│       ├── hash.go            # maphash 哈希函数
│       ├── shmcache.go        # 平台无关接口
│       ├── shmcache_linux.go  # Linux mmap 实现
│       ├── shmcache_other.go  # 非 Linux 桩实现
│       ├── allocator.go       # Slab 内存分配器
│       ├── hashtable.go       # 无锁哈希表（开放地址 + 线性探测）
│       ├── protocol.go        # UDS 二进制协议编解码
│       ├── server.go          # UDS 服务端 + 缓存逻辑
│       ├── client.go          # 客户端封装
│       └── shmcache_test.go   # 单元测试
```

---

## 快速开始

```bash
# 在 Linux 上构建
GOOS=linux GOARCH=amd64 go build ./cmd/shmd

# 启动服务（默认 2GB 共享内存）
./shmd -name shm-go-cache -size 2147483648 -uds "\x00shm-go-cache"

# 使用客户端
```

---

## 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-name` | `shm-go-cache` | 共享内存 segment 名 |
| `-size` | `2GB` | 共享内存大小 |
| `-uds` | `\0shm-go-cache` | UDS 抽象路径 |

---

## 验证

```bash
go test ./pkg/shmcache/ -v -race -count=1
```

---

## 设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 哈希表 | 开放地址 + 线性探测 | CPU cache 友好，无额外指针开销 |
| 内存管理 | Slab 分配器 | O(1) 分配/释放，消除外部碎片 |
| 并发模型 | 单写入者 + 多读者 | 读者零锁、无系统调用 |
| IPC | 二进制 TLV over UDS | 比 gRPC/HTTP 快 10-100x |
| 哈希函数 | Go maphash | 快速、seed 化抗哈希碰撞 |

---

## 适用场景

- **AI 模型推理**：加载 embedding 表、特征字典、tokenizer 词表
- **配置文件热加载**：多进程共享同一份热配置
- **大字典缓存**：词典、映射表、黑白名单
- **跨进程数据共享**：避免重复内存占用
