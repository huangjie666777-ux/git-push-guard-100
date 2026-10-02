# git-push-guard

纯后端 Git 推送凭据拦截服务：通过 Smart HTTP 提供真实 clone/fetch/push，
在 pre-receive 阶段扫描整批引用更新引入的新提交，命中凭据规则即原子拒绝整个推送。

## 架构

| 文件 | 职责 |
|---|---|
| main.go | 启动入口：加载配置、初始化仓库、监听、信号关闭；hook 子命令入口 |
| config.go | JSON 配置加载与校验（规则 ID 唯一、Go 正则编译），非法配置拒绝启动 |
| repo.go | bare 仓库生命周期：首次 git init --bare，重启复用，拒绝覆盖非仓库目录与外来 hook |
| server.go | HTTP 接入（chi）：仅服务配置的仓库名，CGI 调用 git-http-backend，进程组管理 |
| hook.go | pre-receive 逻辑：解析引用更新、对象类型检查、调用扫描 |
| scan.go | 历史遍历（rev-list --not --all）与 blob 内容扫描（缓存、字节预算） |
| gitutil.go | git 子进程执行，全部参数向量，无 shell 拼接 |

## 配置（config.json，见 config.example.json）

- listen：仅允许 loopback 地址（如 127.0.0.1:8080），非回环地址拒绝启动
- repoDir / repoName：仓库目录与仓库名（仅字母数字 . _ -，杜绝路径穿越）
- maxScanBytes：单次推送累计扫描字节预算，超出即拒绝
- scanTimeoutSeconds：扫描超时，超时即拒绝
- rules：id（唯一）+ pattern（Go regexp），逐行匹配新增提交的全部文件内容，
  不按扩展名跳过；相同 blob 只扫一次，但仍按各提交/路径报告

## 行为与边界

- 扫描范围：新引用可达、推送前任何引用均不可达的全部提交（rev-list <new> --not --all），
  覆盖新分支、合并侧支、强推、轻量/附注标签（指向提交者）；中间提交含凭据即使末次已删除仍拒绝
- 指向 blob/tree 的标签直接拒绝；删除引用不引入提交，直接放行
- 命中报告仅含 规则ID、提交、路径、行号，响应与日志均不输出凭据正文
- 在 Git 隔离区（quarantine）内读取新对象；任一命中或出错（读取失败/超预算/超时）
  都在 pre-receive 阶段非零退出，整个推送被拒绝，原引用不变，不存在先发布再回滚
- 客户端断开或服务关闭：HTTP 请求 context 取消即 SIGKILL 整个子进程组
  （git-http-backend → receive-pack → hook → 扫描用 git 子进程）
- 特殊文件名安全：ls-tree -z NUL 分隔解析；所有 git 调用均为参数向量，禁止 shell 拼接
- 仅服务配置的单个仓库，其他路径一律 404，无法访问配置目录外的仓库

## 运行

    go build -o bin/git-push-guard .
    cp config.example.json config.json   # 按需修改
    ./bin/git-push-guard --config config.json

    git clone http://127.0.0.1:8080/repo.git
    # 正常提交可推送；含凭据的推送被 pre-receive 拒绝并报告 rule/commit/path/line

## 测试

    go test ./...

覆盖：配置校验（非法正则/重复ID/路径穿越仓库名/空规则）、仓库初始化与复用、
拒绝非仓库目录、中间提交含凭据、字节预算、hook 拒绝报告不泄露正文、
干净推送与删除放行、拒绝 blob 标签。
