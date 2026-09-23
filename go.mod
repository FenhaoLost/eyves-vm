module eyves

go 1.25.0

// 依赖策略（强制项）：
//   - 本模块当前零外部依赖，纯标准库，保证 CGO_ENABLED=0 静态构建。
//   - 引入任何新依赖必须走架构评审，并在 docs/deps.md 记录：
//     用途 / 替代方案 / 维护状态 / License。
//   - 测试仅用标准库 testing + 自建轻量断言，避免为单包引入 testify。