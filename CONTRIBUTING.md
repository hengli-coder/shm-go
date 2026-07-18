# Contributing to shm-go

感谢你考虑为 shm-go 做贡献！

## 开发环境

### 前置要求

- Go 1.25 或更高版本
- Linux 系统（共享内存功能仅支持 Linux）
- golangci-lint（用于代码检查）

### 安装 golangci-lint

```bash
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin
```

## 开发流程

### 1. Fork 并 Clone

```bash
git clone https://github.com/<your-username>/shm-go.git
cd shm-go
```

### 2. 创建分支

```bash
git checkout -b feature/your-feature-name
```

### 3. 进行修改

确保代码通过所有测试和 lint 检查：

```bash
# 运行测试
make test

# 运行 lint
make lint

# 运行 benchmark
make bench
```

### 4. 提交代码

我们使用类似 [Conventional Commits](https://www.conventionalcommits.org/) 的提交风格：

- `feat:` 新功能
- `fix:` Bug 修复
- `docs:` 文档更新
- `test:` 测试相关
- `refactor:` 代码重构
- `chore:` 杂项（构建、配置等）

示例：

```
feat: add TTL support for cache entries
fix: correct hash table probing on collision
docs: add usage examples for client API
```

### 5. 推送并创建 PR

```bash
git push origin feature/your-feature-name
```

然后在 GitHub 上创建 Pull Request。

## 代码风格

### 命名规范

- 遵循 [Go 命名规范](https://go.dev/doc/effective_go#names)
- 导出的函数和类型必须有文档注释
- 使用 `MixedCaps` 而非下划线

### 文档注释

导出的标识符必须有 godoc 注释：

```go
// CacheClient is a client for the shared memory cache.
// It connects to the server via UDS and reads data directly from shared memory.
type CacheClient struct {
    // ...
}
```

### 错误处理

- 不要忽略错误
- 使用 `errors.New()` 或 `fmt.Errorf()` 创建错误
- 适当使用错误包装（`%w`）

## 项目结构

```
shm-go/
├── cmd/
│   └── shmd/           # Daemon 入口
├── pkg/
│   └── shmcache/       # 核心库
├── examples/           # 使用示例
├── .github/
│   └── workflows/      # CI 配置
└── Makefile            # 构建命令
```

## 测试

### 单元测试

```bash
make test
# 或
go test ./pkg/shmcache/ -v -count=1
```

### 测试覆盖率

```bash
make coverage
```

### Benchmark

```bash
make bench
# 或
go test ./pkg/shmcache/ -bench=. -benchmem
```

## 安全问题

如果你发现了安全漏洞，请**不要**在公开的 Issue 中报告。
请参考 [SECURITY.md](SECURITY.md) 了解安全问题的报告流程。

## 行为准则

- 尊重所有贡献者
- 保持讨论专业和友善
- 接受建设性批评

## 许可证

通过提交代码，你同意你的贡献将按照 Apache 2.0 许可证授权。