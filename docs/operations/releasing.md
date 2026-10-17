# CI 与发版

## 源码流向

维护者只推送 `ssh://git@ssh.via.moe/dejavu/TAdmin.git`。Forgejo 将 master 和标签同步到 GitHub，GitHub Actions 执行 CI。不要添加 GitHub 写入远程，也不要直接合并镜像上的 PR；贡献由维护者先应用到 Forgejo。

普通 master 推送只检查和构建。发布由另行授权的稳定版本标签触发，不自动部署服务器，当前也不假定已有发行版。

## 检查范围

| 检查 | 作用 |
|---|---|
| gofmt、模块 verify/tidy、go vet | 格式、模块完整性和静态问题 |
| go test -race ./... | 并发、业务回归和离线 Jev 提示消融 |
| govulncheck | Go 标准库及依赖的已知可达漏洞 |
| 许可证同步检查 | AMD64/ARM64 运行依赖、Go 运行库和内嵌字体声明 |
| pnpm audit、frozen lockfile | 文档依赖，中等及以上漏洞失败 |
| CodeQL（Go、JS/TS、Python、Actions） | 源码与流水线安全分析 |
| Gitleaks 全历史 | 凭据检查，报告脱敏 |
| actionlint | 工作流语法、表达式及 shell 检查 |
| docgen -check、VitePress、Python unittest | 文档同步与离线评测工具 |
| AMD64/ARM64 镜像 smoke | 非 root、只读根目录、离线自检、备份完整性、禁止覆盖及隔离恢复 |
| PR dependency review | 依赖变更引入的漏洞 |

每周定时查询新的安全公告，不重新调用模型。没有自动依赖合并、自动版本号、服务器部署或重复的 Forgejo CI。

首次镜像后确认 Settings 中的 Actions、dependency graph、Dependabot alerts、CodeQL code scanning 和私密漏洞报告可用，不重复启用 CodeQL default setup。CodeQL 分析成功不等于零告警，应检查 Security 页；按可用规则集要求严重告警处理、`CI / acceptance` 和 `Dependency review / review`。单向镜像推送需要与分支规则协调，不能把本地检查当成 GitHub 已验收。

## 本地命令

Linux 源码目录执行；Windows 维护者通过 AGENTS 指定的 WSL runner 运行 Linux 部分：

```sh
go mod verify
go vet ./...
go test -race ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
bash scripts/licenses.sh /tmp/THIRD_PARTY_NOTICES.txt
diff -u THIRD_PARTY_NOTICES.txt /tmp/THIRD_PARTY_NOTICES.txt
go run ./cmd/docgen -check
python3 -m unittest discover -s deploy/laya -p 'test_*.py'
pnpm install --frozen-lockfile
pnpm audit --audit-level=moderate
pnpm docs:build
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/*.yml
docker buildx build --platform linux/amd64 --load --build-arg VERSION=ci -t tadmin:ci .
bash scripts/verify-image.sh tadmin:ci ci
docker buildx build --platform linux/arm64 --load --build-arg VERSION=ci -t tadmin:ci-arm64 .
PLATFORM=linux/arm64 bash scripts/verify-image.sh tadmin:ci-arm64 ci
```

异构镜像的运行检查需要 QEMU/binfmt，不能把交叉编译当作 ARM64 实际运行。Gitleaks 在含完整 Git 历史的 Windows 正本执行；WSL 镜像不含 .git。工具链或源码清单改变后先 `go run ./cmd/docgen` 更新生成数据，再检查。

## 发布步骤

1. GitHub 上 master 的准确提交完成全部 CI，核对镜像 SHA 与 Forgejo 一致。
2. 复核 CodeQL、依赖、变更记录和 [真实群验收](/operations/acceptance)。本地通过不能替代。
3. 明确授权版本后，在 Windows 正本创建稳定标签，例如 `v2.0.0`，仅推送到 Forgejo；确认标签同步包括目标提交。
4. 标签 CI 再跑全部检查并确认该提交属于 master，然后生成 Linux AMD64/ARM64 二进制、SHA256SUMS、二进制构建证明和带 SBOM/provenance 的 GHCR 多架构镜像。
5. 检查 Release 资产、GHCR digest、两个平台清单和匿名公开拉取。不要移动已发布标签，也不要把发布成功当成部署成功。

只生成精确版本镜像标签，不维护隐含升级的 latest。失败后先检查已生成的产物，不覆盖发布版本；修复用新版本。校验及证明验证方法见 [部署指南](/guide/deployment)。
