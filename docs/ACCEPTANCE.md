
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

## v2.1.0 增量验收（2026-09-26，Go 版多引擎集成）

- [x] 引擎发现重构 DetectEngines：engines/ 根（rapfi 兼容旧布局）+ jax/katagomo/alphagomoku 子目录
- [x] RapfiAI 参数化：Kind/Exe 候选/JaxDevice；JAX configs/config.toml 的 [eval] device 可设 cpu/cuda/tensorrt
- [x] readline 过滤 DEBUG/UNKNOWN 行（JAX 调试输出）
- [x] 回退链：选定引擎缺失或启动失败 -> rapfi -> simple（日志写明原因）
- [x] 实测：AlphaGomoku MK 拒绝 13x13（"Only 15x15 or 20x20"）-> 集成但文档标注不适用；
      JAX 13x13 出招正常（cpu 模式 2.9s 含模型加载）；device=cuda 在无 CUDA 11.8
      运行时的机器上初始化挂起 -> 默认 gpu_device=cpu，UI 提示自装运行时后可切
- [x] ListEngines 绑定：UI 引擎下拉动态显示可用性（未安装置灰）

## v2.1.1 增量验收（2026-09-26，Go 版）

- [x] 修复 GPU 设备下拉选不上：onchange 先 save 更新 settings 再刷新可见性，
      updateDeviceVisibility 不再覆盖用户刚选的值；GPUDevice 变化触发引擎重启
      （JAX device patch 在启动时执行，切换设备必须重启才生效）
- [x] 点击未生效诊断：失败时自动保存现场截图（debug/click_fail_*.png）；
      若最近识别已丢失棋盘网格则提示"对局可能已结束或界面变化"
      （配合日志 Eval -M2 可判断是终局而非点击 bug）

## v2.1.2 增量验收（2026-09-26，Go 版）

- [x] 开局重试：空盘且自动开启时，每 30s 重试中心开局（上限 10 次），
      覆盖"先开棋盘界面、对局稍后才真正开始"的场景；
      任何落子观测后重置计数
- [x] 点击日志带屏幕绝对坐标（已点击 G7 @屏幕(x,y)），偏移类问题可直接核对
- [x] Wails 前端补接线 window/our_color 事件（此前状态栏停留在
      "正在查找游戏窗口…"造成误判）
- [x] 现场截图证实 v2.1.1 的点击失败实为"对局未开始/未轮到我方"，
      程序行为符合设计（游戏忽略非本回合点击）

## v2.1.3 增量验收（2026-09-26，Go 版）

- [x] "鼠标到位但点击无效"：新增三种点击策略自动升级——
      标准 SendInput（光标+DOWN/UP）→ SendInput 批量绝对移动+DOWN →
      窗口消息 WM_LBUTTONDOWN/UP 直投（免前台，客户区坐标）
- [x] 每次重试自动升级策略并记录所用方式（标准/批量/窗口消息）
- [x] 已知触发场景：对局未开始/未轮到我方时游戏忽略点击（属正常），
      回退链会等待；本策略升级针对"游戏吞标准单击"的对局
