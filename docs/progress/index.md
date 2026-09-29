<script setup>
import progress from '../.vitepress/data/generated/progress.json'
</script>

# 项目进度

<p class="lead">{{ progress.headline }}</p>

这一页的每一项“已验证”都指向仓库中真实存在的测试函数——测试被删除或改名，文档生成就会失败。标记为“待实机验收”的功能已离线测试通过，但依赖真实 Telegram 客户端或服务端的行为，需要按 [上线验收清单](/operations/acceptance) 在测试群中确认。

<ProgressBoard />

## 如何更新这一页

1. 编辑仓库中的 `docs/progress.toml`：调整功能状态、补充关联测试或里程碑。
2. 运行 `go run ./cmd/docgen` 重新生成数据。
3. `go test ./internal/docgen` 通过即说明引用的测试都存在、状态值合法。
