# Git Push Guard

纯后端的 Git 推送凭据拦截服务。服务仅监听 `127.0.0.1`，通过 Smart HTTP
（`git http-backend`）支持真实的 `clone` / `fetch` / `push`，并在接收对象的
quarantine（隔离区）阶段运行 `pre-receive` 钩子；一旦命中规则，整批引用更新被
拒绝，原引用保持不变，对象随后随隔离区一并丢弃，不存在“先发布再回滚”。

## 配置

通过 `--config` 指定 JSON 文件（见 `config.example.json`）：

| 字段 | 说明 |
| --- | --- |
| `port` | 本机监听端口，必须为 1–65535。 |
| `repo_dir` | 唯一 bare 仓库目录；首次启动自动 `git init --bare`，重启复用。 |
| `scan_timeout_seconds` | 单次推送扫描超时，默认 30 秒。 |
| `max_scan_bytes` | 本次推送可扫描的唯一 blob 总字节预算，默认 256 MiB。 |
| `git_bin` | Git 可执行文件路径，默认 `git`。 |
| `rules` | 非空数组；每项含唯一的 `id` 和 Go RE2 语法的 `regexp`（字符串）。 |

规则 ID 重复、正则非法、端口非法、规则为空等配置错误都会导致进程拒绝启动。

## 运行

```sh
go build -o bin/git-push-guard .
./bin/git-push-guard --config config.json
# 仓库地址：http://127.0.0.1:<port>/<repo目录名>

git clone http://127.0.0.1:8080/main.git
git push http://127.0.0.1:8080/main.git main
```

服务启动时把自身二进制复制为仓库的 `hooks/pre-receive`（由
`GIT_PUSH_GUARD_CONFIG` 环境变量定位配置），因此本地直接 `git push` 到该
bare 目录时钩子同样生效。

## 扫描语义与边界

- `pre-receive` 读取整批 `old new ref` 记录统一处理，而非逐引用处理。
- 新提交集合 = 新引用/新标签可达，但推送前任何引用都不可达的提交：
  `git rev-list <新提示提交> --not --all`。在 quarantine 环境中旧对象通过
  `GIT_ALTERNATE_OBJECT_DIRECTORIES` 可见、新对象尚不在主对象库，`--all`
  仍只包含旧引用，因此覆盖新分支、合并进来的侧支与强推场景。
- 支持指向提交的轻量标签与附注标签（`new^{commit}` 可解析）；指向 blob/tree
  的标签直接拒绝；删除引用不引入提交，直接跳过。
- 每个新提交相对其全部父提交（根提交相对空树）枚举新增/修改文件，逐行匹配，
  不按扩展名或 MIME 跳过；同一 blob OID 在一次推送内只扫描一次，但会保留该
  blob 出现过的全部提交与路径并分别报告。
- 中间提交包含凭据、末次提交删除时仍会拒绝。
- 命中输出与服务端日志只包含规则 ID、提交 SHA、路径和行号，绝不输出匹配到的
  凭据正文。
- 对象读取失败、blob 总大小超过 `max_scan_bytes`、扫描超过超时时间都会拒绝整
  个推送。
- 文件名通过 `diff-tree -z` 的 NUL 分隔记录解析，安全支持空格、引号、换行等
  特殊文件名；所有 Git 调用均以参数数组执行，不拼接 shell 命令。
- HTTP 层仅白名单挂载配置仓库的 `info/refs`、`git-upload-pack`、
  `git-receive-pack`，其他路径一律不转发给 `git-http-backend`。
- Git 与扫描子进程均在独立进程组中启动；客户端断开（请求 context 取消）或
  服务关闭时，会向整个进程组发送 `SIGKILL`，不会遗留后台 Git 进程。

## 测试

```sh
go test ./...
```

端到端测试使用系统 `git` 真实执行 init/clone/push/merge/tag，并通过测试二进制
以 `pre-receive` 模式自举钩子，覆盖：中间提交泄密、合并侧支、强推引用不变、
轻量/附注标签、非提交标签拒绝、HTTP clone/fetch/push、仓库目录外访问拒绝。
