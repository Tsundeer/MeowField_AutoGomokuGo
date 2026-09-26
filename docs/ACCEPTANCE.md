
## v2.0.1 增量验收（2026-09-26，Go 版）

- [x] 接管体验重做：轮次未知（双方子数相等）时按"我方先行"立即出手一次；
      点击未生效自动回退等待（不再要求用户手动点好几盘才启动引擎）
- [x] 点击偏移自动校准：确认格与点击格不一致（±2 格内）时记录像素偏移并修正后续点击；
      另提供手动偏移设置（click_offset_x/y）
- [x] 进程级 DPI 感知（per-monitor v2 -> shcore -> SetProcessDPIAware 回退），
      修复缩放显示/多显示器下点击整体偏移
- [x] 思考预算必下发（每步 INFO timeout_turn），并在日志记录"思考预算 X.Xs"
- [x] 落子停顿下拉框值归一化（修复显示空白与 NaN）；Rapfi 日志去双前缀、Depth 明细不再刷屏

## v2.0.2 增量验收（2026-09-26，Go 版）

- [x] 根因定位："到我方下棋却不动" = Rapfi 找到深层必胜（如 +M29）后进入
      必胜证明模式，num_iteration_after_mate=24 允许追加 24 层迭代且豁免
      时间限制（用户设 5s 但 Depth 38 搜了 18s+ 不出招）。
      A/B 对照实验证实 timeout 机制本身正常（普通局面 5s 准时出招）。
- [x] 修复1：引擎启动前 patch config.toml，num_iteration_after_mate 24->4
      （找到必胜后快速收尾出招）；线程数同步写入
- [x] 修复2：readline 重构为常驻 reader goroutine + channel（消除读竞争）；
      预算超时后发 STOP 请求引擎交出当前最优着法（协议标准做法）+10s 宽限
- [x] 修复3：MESSAGE Depth 明细不再写日志（v2.0.1 的该补丁因 replace 无断言
      实际未落盘，本轮已用 assert 验证）；去 [rapfi] 双前缀
- [x] 新增测试：TestPatchEngineMate / TestRapfiStopFallback（0.5s 预算实测 <15s）

## v2.0.3 增量验收（2026-09-26，Go 版）

- [x] 「必胜快速落子」开关（默认开）：开=num_iteration_after_mate 4（找到必胜快速出招）；
      关=24（完整证明，更强但耗时可能远超思考上限）；切换后引擎自动重启生效
- [x] 必胜检测日志：解析搜索行 Eval ±Mxx，首次发现必胜线时写明
      "引擎发现必胜线 +M29：正在快速收尾/完整证明模式…"，消除"卡死"误解
- [x] 同批差分停摆回归、配置补丁双态测试、STOP 兜底测试全绿
