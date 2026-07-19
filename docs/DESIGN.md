# featcache: AI 特征向量的零拷贝运行时缓存

> 版本：v2 | 日期：2026-07-19

---

## 1. 产品定位

**featcache** 是一个面向 AI 推理场景的零拷贝运行时数据缓存。它解决的核心问题是：

> **AI 推理进程启动时，大量静态数据（Embedding、Tokenizer、Feature Dictionary 等）的重复加载导致启动慢、资源浪费。**

### 1.1 一句话

```
featcache = 一次加载 + 多进程零拷贝共享 + 热切换
```

### 1.2 适用场景

| 场景 | 数据示例 | 典型大小 |
|------|---------|---------|
| 推荐系统 | User/Item Embedding 表 | 10GB~100GB |
| LLM 推理 | Tokenizer 词汇表、BPE 编码 | 1GB~10GB |
| 多模态模型 | 图像/文本特征字典 | 5GB~50GB |
| 广告 CTR 预估 | 稀疏特征字典、Lookup 表 | 10GB~30GB |
| RAG 系统 | 文档 Embedding 库 | 10GB~100GB |
| 搜索引擎 | ANN 索引、倒排表 | 5GB~50GB |

这类数据的共同特征：

- **大**：GB ~ TB 级
- **只读**：运行时不变，或更新频率很低
- **生命周期长**：进程启动到结束一直存在
- **多进程共享**：同机多个推理进程都需要访问

### 1.3 核心价值

| 维度 | 传统方案（各进程独立加载） | featcache |
|------|---------------------------|-----------|
| 启动延迟 | 数分钟（加载 10GB+ 数据） | < 100ms（mmap + 查元数据） |
| 内存占用 | N × 数据量（N 个进程各一份） | 数据量 + 小索引（共享一份） |
| 查询延迟 | 本地内存读取 | 本地内存读取（零拷贝，相同量级） |
| 资源消耗 | 加载时占满带宽/CPU | 加载一次，后续零开销 |
| 热更新 | 需要重启进程 | 双缓冲切换（二期） |

---

## 2. 架构概览

### 2.1 角色定义

```
┌──────────────────────────────────────────────────────────┐
│                     featcache 守护进程                      │
│  = Loader / Artifact Manager                              │
│                                                          │
│  ▲ 唯一写入者                                             │
│  ▲ 从数据源加载推理工件（Embedding / Tokenizer / ...）      │
│  ▲ 写入内存段，构建索引                                    │
│  ▲ 通过 UDS 提供控制面服务                                  │
│  ▲ 支持版本管理和热切换                                     │
└──────────────┬───────────────────────────────────────────┘
               │  shm_open + mmap
               ▼
┌──────────────────────────────────────────────────────────┐
│                    内存段 (Memory Segment)                 │
│                                                          │
│  ┌──────────────────────────────────────────────────┐    │
│  │  数据面 (Data Plane)                              │    │
│  │  • 索引区域：开放寻址哈希表，O(1) 查找              │    │
│  │  • 数据区域：紧凑连续存储 key + value（任意 blob）  │    │
│  │  • 零拷贝读取，无锁，无 syscall                     │    │
│  └──────────────────────────────────────────────────┘    │
│                                                          │
│  控制面 (Control Plane) → Unix Domain Socket             │
│  • 客户端初始化时获取段元数据                              │
│  • 版本变更时通知客户端切换                                │
└──────────────┬───────────────────────────────────────────┘
               │  mmap（所有进程共享同一物理内存）
               ▼
┌──────────────────────────────────────────────────────────┐
│  推理进程 0    推理进程 1    推理进程 2  ...  推理进程 N    │
│                                                          │
│  ▲ 只读访问（PROT_READ）                                   │
│  ▲ 直接读内存段，零拷贝                                    │
│  ▲ 查询延迟 ≈ 本地 map 读取                                │
│  ▲ 无锁，无 syscall，无 UDS 通信                          │
└──────────────────────────────────────────────────────────┘
```

### 2.2 启动流程

```
时间轴
│
├─ [Loader 启动]
│   ├─ 打开数据源（磁盘文件 / 数据库 / 对象存储）
│   ├─ 创建内存段（shm_open + mmap）
│   ├─ 写入数据 → 构建哈希索引
│   └─ 监听 UDS（就绪）
│
├─ [推理进程 1 启动]
│   ├─ 连接 UDS，请求元数据（段名称、大小、布局）
│   ├─ mmap 内存段
│   ├─ 关闭 UDS（后续查询不走 UDS）
│   └─ 就绪，< 100ms
│
├─ [推理进程 2 启动]  ← 同上，< 100ms
│
├─ [推理进程 N 启动]  ← 同上，< 100ms
│
└─ 所有进程共享同一份物理内存，独立读取
```

### 2.3 热更新流程（第二期）

```
旧版本数据
┌──────────────────┐
│ Segment v1       │  ← 推理进程正在读取
│ GenCounter = 100 │
└──────────────────┘

1. Loader 创建新段
   ┌──────────────────┐
   │ Segment v2       │  ← 写入新数据，构建新索引
   │ GenCounter = 101 │
   └──────────────────┘

2. Loader 通过 UDS 广播版本变更

3. 推理进程收到通知
   - mmap 新段
   - 原子切换内部指针
   - 旧段保留（处理进行中的查询）

4. 所有进程切换完成 → Loader 回收旧段
```

---

## 3. 内存布局

### 3.1 总体布局

```
Offset 0:      ┌─────────────────────────────────────┐
               │  Header (64B)                       │
               │  - Magic, Version, Size             │
               │  - GenCounter                       │
               │  - HashOffset, HashCap, DataOffset  │
               │  - SourceID                         │
               ├─────────────────────────────────────┤
               │  索引区域                             │
               │  开放寻址哈希表，线性探测               │
               │  每个 slot 24B                       │
               │  slot 数 = 2^N                       │
               ├─────────────────────────────────────┤
               │  数据区域                             │
               │  连续存储 key + value                 │
               │  [keyLen:4B][key:keyLen][val:vLen]   │
               │  紧凑排列，无内部碎片                   │
               │  写入时 CAS 推进 data_end             │
               └─────────────────────────────────────┘
```

### 3.2 设计要点

- **紧凑存储**：数据连续排列，无内部碎片。对 10GB+ 数据量，相比 slab 分配器节省 30%+ 空间
- **append-only 写入**：一次写入，运行期间不变。简化并发模型
- **无锁读取**：读者全部走原子读，无锁无 syscall

### 3.3 Header

```c
struct Header {
    uint32_t magic;       // 0x46454154 ("FEAT")
    uint32_t version;     // 布局版本号
    uint64_t size;        // 段总大小
    uint64_t gen_counter; // 版本计数器（每次数据变更递增）
    uint32_t hash_cap;    // 哈希表 slot 数（2 的幂）
    uint32_t hash_offset; // 哈希表起始偏移
    uint32_t data_offset; // 数据区域起始偏移
    uint32_t data_end;    // 数据写入末尾（原子推进）
    uint32_t segment_id;  // 段标识
    uint32_t reserved[4]; // 保留
    char     source_id[8]; // 数据源标识
};
```

### 3.4 Hash Slot

```c
struct HashSlot {
    uint64_t hash;    // 完整 64-bit hash
    uint32_t offset;  // 数据区域偏移（相对 data_offset）
    uint32_t vlen;    // 值长度（字节）
    uint32_t status;  // 0=empty, 1=used, 2=tombstone
};
// 对齐到 24B，一个 cache line 容纳 2 个 slot
```

存储完整 64-bit hash 而非高 32 位，大幅减少伪命中后的 key 比较（key 通常较长）。

### 3.5 数据区域

```
[数据区域]
  ↓ data_end 原子推进

  Chunk 0:
    [keyLen: uint32][key: keyLen bytes][value: vlen bytes]

  Chunk 1:
    [keyLen: uint32][key: keyLen bytes][value: vlen bytes]

  ...
```

- Value 是**不透明的字节序列**。调用方定义序列化方式
  - 可以是 float32 特征向量（128维 = 512B，256维 = 1KB，768维 = 3KB）
  - 可以是 tokenizer 词汇表（字符串列表）
  - 可以是 BPE 编码（byte→token 映射）
  - 可以是任何其他二进制格式
- 数据连续排列，无额外对齐要求

---

## 4. 核心数据结构

### 4.1 Segment

```go
// Segment 是一个共享内存段的句柄。
// 写入者创建并填充数据；读取者打开后只读访问。
type Segment struct {
    name string  // 段名称（shm_open 名称）
    fd   int     // 文件描述符

    // mmap 映射
    base unsafe.Pointer
    size int

    // 从 Header 解析的布局信息
    hashOffset int
    hashCap    int
    dataOffset int

    // 写入者专用
    dataEnd    *atomic.Int32
    genCounter *atomic.Uint64
}
```

### 4.2 Loader（写入者）

```go
// Loader 是内存段的写入者。
// 它从数据源读取工件数据，写入共享内存，构建索引。
type Loader struct {
    segment   *Segment
    hashTable *HashTable
    source    DataSource

    config LoaderConfig
}

type LoaderConfig struct {
    SegmentName string
    SegmentSize int
    LoadFactor  float64  // 默认 0.5
}
```

### 4.3 Reader（读取者）

```go
// Reader 是内存段的读取者。
// 直接读共享内存中的哈希表和数据，零拷贝，无锁。
type Reader struct {
    segment   *Segment
    hashTable *HashTable

    // 控制面连接（仅用于初始化和版本通知）
    conn    *net.UnixConn
    udsAddr string
}

// Get 根据 key 查找对应的 value。
// 返回的 value 切片直接指向共享内存——调用方不得修改。
// 多个 goroutine 可安全并发调用。
func (r *Reader) Get(key []byte) (value []byte, ok bool)

// GetBatch 批量查找。在共享内存中逐条查找，无额外开销。
func (r *Reader) GetBatch(keys [][]byte) (values [][]byte, results []bool)
```

**Reader 不需要锁**。所有 GET 操作只读共享内存，使用原子加载，多个 goroutine 可安全并发。

---

## 5. 读取流程

### 5.1 单条查询

```
GET(key):
  1. hash = HashKey(key)
  2. idx = hash & (hashCap - 1)
  3. for i = 0; i < hashCap; i++:
       slot = atomic_load(&slots[idx])
       if slot.status == empty:
         return NOT_FOUND
       if slot.status == used && slot.hash == hash:
         key_data = read from data_offset + slot.offset (4B keyLen + key)
         if key_data == key:
           value = read from data_offset + slot.offset + 4 + keyLen
           return value
       idx = (idx + 1) & mask
  4. return NOT_FOUND
```

全过程：无 syscall，无锁，无 UDS 通信。仅 1-2 次原子读 + hash 比较。

### 5.2 读安全保证

写入者保证写入顺序：

```
1. 先将 key + value 写入数据区域（普通 store）
2. 再通过原子 store（release 语义）设置哈希表 slot
```

读者按以下顺序操作：

```
1. 原子加载 slot.status（acquire 语义）
2. 如果 status == used，读取 slot.offset 和 slot.vlen
3. 从数据区域读取 key，验证匹配
4. 读取 value
```

读者要么看到空 slot（数据未就绪），要么看到完整的已写入数据，不会读到半写状态。

---

## 6. 写入流程

### 6.1 初次加载

```
1. 计算哈希表大小
   - slot_count = next_power_of_2(expected_entries * 2)  // 负载因子 50%
   - hash_table_size = slot_count * 24

2. 计算区域偏移
   - hash_offset = 64 (Header 之后)
   - data_offset = align8(hash_offset + hash_table_size)

3. 初始化 Header
   - data_end = data_offset

4. 逐条写入
   for each (key, value):
     hash = HashKey(key)
     // 分配数据空间
     chunk_size = 4 + len(key) + len(value)
     chunk_off = cas_advance(&data_end, chunk_size)
     // 写入数据
     write(data_offset + chunk_off, key_len, key, value)
     // 插入哈希表
     insert_into_hashtable(hash, chunk_off, len(value))

5. 更新 gen_counter
```

### 6.2 更新/删除（第二期）

更新和删除通过版本切换实现（见第 9 章热更新），不进行原地修改。简化一期实现，避免墓碑堆积和空间泄漏问题。

---

## 7. 控制面协议（UDS）

### 7.1 设计原则

- **控制面与数据面分离**：UDS 只用于初始化、元数据查询、版本通知
- **数据面零拷贝**：所有数据读取走共享内存，不走 UDS
- 协议轻量，连接即用

### 7.2 OpCode 定义

一期实现：

| OpCode | 值 | 说明 |
|--------|----|------|
| GET_INFO | 0x01 | 获取内存段元数据（名称、大小、布局） |
| GET_STATUS | 0x02 | 获取加载器状态 |

二期扩展（预留）：

| OpCode | 值 | 说明 |
|--------|----|------|
| WATCH_VERSION | 0x03 | 监听版本变更通知 |
| PIN | 0x04 | 固定数据到内存（多级存储时） |
| PREFETCH | 0x05 | 预取数据到缓存层 |
| EVICT | 0x06 | 淘汰缓存数据 |
| LIST | 0x07 | 列出已加载的数据集 |
| RELOAD | 0x08 | 触发重新加载 |

### 7.3 请求格式

```
OpCode:   1B
KeyLen:   2B (uint16, big-endian)
Body:     key bytes (KeyLen)
```

### 7.4 响应格式

```
Status:    1B
  0x00 = OK
  0x01 = NOT_FOUND
  0x02 = BUSY       // 正在加载/重新加载
  0x03 = ERROR

SegmentName: 64B (固定长度，空字符填充)
SegmentSize: 8B  (uint64, big-endian)
HashOffset:  4B  (uint32, big-endian)
HashCap:     4B  (uint32, big-endian)
DataOffset:  4B  (uint32, big-endian)
GenCounter:  8B  (uint64, big-endian)
```

### 7.5 客户端初始化

```
1. 连接 UDS（抽象命名空间，如 "\x00featcache-<name>"）
2. 发送 GET_INFO
3. 接收响应，获取段元数据
4. mmap 共享内存段
5. 之后所有查询走共享内存，不再使用 UDS
```

---

## 8. 数据源抽象

```go
// DataSource 定义了数据源的接口。
// 实现：FileDataSource / DatabaseDataSource / StreamDataSource
type DataSource interface {
    // Open 打开数据源，返回总条目数（用于预估哈希表大小）
    Open() (totalEntries int, err error)

    // Next 读取下一条记录
    Next() (key []byte, value []byte, err error)

    // Close 关闭数据源
    Close() error
}
```

Value 是任意字节序列。调用方负责序列化：
- 特征向量：`encoding/binary` 或直接 `[]float32` 的底层字节
- Tokenizer 词汇表：JSON / protobuf
- 任意自定义格式

---

## 9. 热更新（第二期，概要）

### 9.1 双缓冲方案

```
1. Loader 创建新内存段，写入新版本数据
2. Loader 通过 UDS 通知所有 Reader（WATCH_VERSION）
3. 每个 Reader 收到通知后：
   a. mmap 新段
   b. 原子替换内部指针
   c. 旧段保留（处理进行中的查询）
4. 所有 Reader 确认切换后，Loader 回收旧段
```

### 9.2 注意事项

- 热更新期间查询可能短暂访问旧数据（最终一致性）
- 旧段回收需要引用计数或定时器
- 数据源差异检测（仅更新变更的 key）

---

## 10. 哈希表

### 10.1 算法

- **开放寻址 + 线性探测**
- 完整 64-bit hash 存储，减少伪命中 key 比较
- 负载因子 ≤ 50%，保证查找性能

### 10.2 并发安全

- 写入者 CAS 标记 slot 状态
- 读者原子加载 slot 状态（跨架构安全，不仅限于 x86）
- slot 写入顺序保证：数据先就绪，slot 后标记

### 10.3 性能

负载因子 50% 时：
- 平均探测次数 ≈ 1.5 次
- 99% 查找在 5 次探测内完成
- 每次探测 = 1 次原子读 + 1 次 hash 比较
- hash 匹配时额外 1 次 key 比较

---

## 11. 性能预期

| 指标 | 预期值 | 说明 |
|------|--------|------|
| 推理进程启动延迟 | < 100ms | 无论数据量多大（10GB / 100GB） |
| 单次查询延迟 | < 100ns | 1-2 次原子读 + hash 比较 |
| 批量查询延迟 | N × 单次延迟 | 线性扩展，无额外开销 |
| 内存效率 | 数据量 × ~1.002 | 紧凑存储，哈希表开销极小 |
| 多进程内存节省 | (N-1) × 数据量 | N 个进程共享一份数据 |

---

## 12. 设计决策记录

### ADR-1: 为什么不用 slab 分配器？

**决策**：紧凑存储，append-only 写入。

**理由**：
- 一期场景"一次写入，只读不写"，不需要动态分配
- 紧凑存储消除内部碎片，10GB+ 数据量节省显著
- 热更新通过创建新段 + 原子切换实现，旧段整体回收

### ADR-2: 为什么 slot 用 24B 存储完整 64-bit hash？

**决策**：24B slot，存储完整 hash。

**理由**：
- 完整 hash 比较大幅减少 key 比较（key 通常较长）
- 两个 slot 占 48B，在一个 cache line 内，预取友好
- 额外 8B/slot，百万条目多 8MB，相对于 10GB 数据量可忽略

### ADR-3: 为什么客户端直接查哈希表，不走 UDS？

**决策**：客户端本地查共享内存哈希表。

**理由**：
- 消除 UDS 往返（微秒级 → 纳秒级）
- 消除服务端瓶颈
- N 个客户端并发查询不增加服务端压力
- UDS 仅在初始化时使用

### ADR-4: 一期为什么不做 GC？

**决策**：一期不实现空间回收。

**理由**：
- 一期"一次加载，运行期间不变"，无 DELETE/UPDATE
- 二期热更新通过"创建新段 + 原子切换"替换数据
- 原地 GC 需要引用计数或标记清除，复杂度高

---

## 13. 生态调研

### 13.1 Safetensors (HuggingFace)

| 维度 | 说明 |
|------|------|
| 定位 | 模型权重文件格式 |
| 与 featcache 的关系 | **输入格式之一**。Loader 可实现 SafetensorsDataSource，直接从 .safetensors 文件读取特征向量 |
| 关键区别 | 每个进程各自 mmap 同一文件，各自建立映射。featcache 做到"一次加载，多进程共享" |
| 参考价值 | 低。存储格式 vs 运行时缓存 |

### 13.2 vLLM / PagedAttention

| 维度 | 说明 |
|------|------|
| 定位 | LLM 推理引擎，GPU 显存管理 |
| 与 featcache 的关系 | 解决类似问题（启动慢、资源复用），技术栈不同（GPU CUDA vs CPU shm） |
| 参考价值 | 中。"一次加载，多请求共享"的理念一致，实现不同 |

### 13.3 FAISS (Meta)

| 维度 | 说明 |
|------|------|
| 定位 | 高效向量相似度搜索 |
| 与 featcache 的关系 | **最接近的参考实现**。FAISS 支持 mmap 共享索引，多进程零拷贝读取 |
| 关键区别 | FAISS 是向量搜索（KNN/ANN），featcache 是 key-value 精确查找 |
| 参考价值 | **高**。mmap 共享大数据给多进程的方案被生产验证 |

### 13.4 Apache Arrow / Plasma

| 维度 | 说明 |
|------|------|
| 定位 | 跨进程内存数据共享框架 |
| 与 featcache 的关系 | 理念最接近。Plasma store 是共享内存对象存储 |
| 为什么不用 | 已被废弃，复杂度高、维护成本大、依赖 C++ 运行时 |
| 参考价值 | **高（反面参考）**。验证了需求真实存在，但因复杂度过高而失败。featcache 以简洁性形成对比 |

### 13.5 Redis

| 维度 | 说明 |
|------|------|
| 定位 | 内存 KV 存储 |
| 与 featcache 的关系 | 功能全但性能差一个量级（网络通信 vs 共享内存） |
| 参考价值 | 中。跨机器场景可作为远程缓存层配合使用 |

### 13.6 总结

| 维度 | 现有方案 | featcache |
|------|---------|-----------|
| 零拷贝多进程共享 | ❌ 无完整方案 | ✅ 专为此设计 |
| 纳秒级查询延迟 | ❌ Redis 等走网络 | ✅ 直接读共享内存 |
| 纯 Go，无外部依赖 | ❌ Plasma/FAISS 依赖 C++ | ✅ 仅依赖 `golang.org/x/sys` |
| 抽象数据源接口 | ❌ 绑定特定格式 | ✅ DataSource 接口 |
| 热更新 | ❌ 需重启进程 | ✅ 双缓冲切换（二期） |
| 10GB+ 优化 | ❌ 通用方案有额外开销 | ✅ 紧凑存储 |

---

## 14. 未来扩展

| 方向 | 说明 | 优先级 |
|------|------|--------|
| 多级存储 | GPU → RAM → NVMe → Object Store 自动分层 | 三期 |
| 持久化 | 数据落盘，重启恢复 | 三期 |
| 监控指标 | Prometheus 指标：命中率、延迟、内存使用 | 三期 |
| 分布式 | 多机共享，一致性哈希路由 | 远期 |
| 压缩存储 | 特征向量压缩，减少内存占用 | 远期 |

---

## 15. 实现计划

### Phase 1: 核心功能（一期）

1. 实现 Segment 管理（创建/打开/关闭/销毁）
2. 实现 Header 读写
3. 实现 HashTable（完整 64-bit hash，开放寻址）
4. 实现 Loader（批量加载数据）
5. 实现 Reader（直接读共享内存，零 UDS 往返）
6. 实现 UDS 控制面协议（GET_INFO / GET_STATUS）
7. 数据源抽象：DataSource 接口 + FileDataSource 实现
8. 测试 + 基准测试

### Phase 2: 热更新

1. 双缓冲版本切换
2. WATCH_VERSION 协议
3. 增量更新 + 差异检测
4. 旧段引用计数和回收

### Phase 3: 增强

1. 多级存储
2. 持久化
3. 监控指标