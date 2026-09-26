// MeowField_AutoGomokuGo 前端逻辑（Wails 绑定 + 棋盘渲染）。
async function buildEngineList() {
    const list = await ListEngines();
    const sel = $("selEngine");
    sel.innerHTML = "";
    for (const e of list) {
        const o = document.createElement("option");
        o.value = e.name;
        o.textContent = e.available ? e.name : `${e.name}（未安装）`;
        if (!e.available) o.style.color = "#888";
        sel.appendChild(o);
    }
    sel.value = settings.engine || "auto";
    if (sel.selectedIndex === -1) sel.value = "auto";
    updateDeviceVisibility();
}

function updateDeviceVisibility() {
    const isJax = $("selEngine").value === "jax";
    $("lblDevice").style.display = isJax ? "" : "none";
    $("selDevice").style.display = isJax ? "" : "none";
    if (isJax) $("selDevice").value = settings.gpu_device || "cuda";
}

function fmt1(v) {
    const x = typeof v === "number" ? v : parseFloat(v);
    return Number.isFinite(x) ? (Math.round(x * 10) / 10).toFixed(1) : "1.0";
}
function setSelect(sel, val) {
    sel.value = val;
    if (sel.selectedIndex === -1) {
        const o = document.createElement("option");
        o.value = o.textContent = val;
        sel.appendChild(o);
        sel.value = val;
    }
}

// Wails 桌面端运行时注入的全局绑定
const GetSettings = () => window.go.main.App.GetSettings();
const SaveSettings = m => window.go.main.App.SaveSettings(m);
const StartAuto = () => window.go.main.App.StartAuto();
const StopAuto = () => window.go.main.App.StopAuto();
const TestShot = () => window.go.main.App.TestShot();
const OpenDebugDir = () => window.go.main.App.OpenDebugDir();
const ListEngines = () => window.go.main.App.ListEngines();
const CheckUpdate = () => window.go.main.App.CheckUpdate();
const Version = () => window.go.main.App.Version();
const EventsOn = (name, cb) => window.runtime.EventsOn(name, cb);

const N = 13, MARGIN = 30, PX = 500;
const $ = id => document.getElementById(id);
const colorName = { 1: "黑", 2: "白" };

let settings = {};
let awaiting = [];
let active = false;
const history = [];

// ---- 棋盘渲染 ----
function drawEmpty() {
    const cv = $("board"), ctx = cv.getContext("2d");
    const m = MARGIN, sp = (PX - 2 * MARGIN) / (N - 1);
    ctx.fillStyle = "#EFD9A7"; ctx.fillRect(0, 0, PX, PX);
    ctx.fillStyle = "#EAD2A0";
    for (let i = 1; i < 12; i += 2)
        ctx.fillRect(0, PX * i / 12, PX, PX / 12 + 1);
    ctx.strokeStyle = "#9C7B53"; ctx.lineWidth = 1;
    for (let i = 0; i < N; i++) {
        const p = m + i * sp;
        ctx.beginPath(); ctx.moveTo(p, m); ctx.lineTo(p, m + (N-1)*sp); ctx.stroke();
        ctx.beginPath(); ctx.moveTo(m, p); ctx.lineTo(m + (N-1)*sp, p); ctx.stroke();
    }
    ctx.fillStyle = "#7d5f3f";
    for (const [i, j] of [[3,3],[3,9],[9,3],[9,9],[6,6]]) {
        const x = m + j*sp, y = m + i*sp;
        ctx.beginPath(); ctx.arc(x, y, Math.max(2, sp*0.09), 0, 7); ctx.fill();
    }
    // 坐标
    ctx.fillStyle = "#7a5c38"; ctx.font = "9px Segoe UI";
    for (let i = 0; i < N; i++) {
        ctx.textAlign = "center";
        ctx.fillText(String.fromCharCode(65+i), m + i*sp, MARGIN - 8);
        ctx.fillText(String(i+1), MARGIN - 12, m + i*sp + 3);
    }
    return { m, sp };
}

function drawBoard(payload) {
    const { m, sp } = drawEmpty();
    const ctx = $("board").getContext("2d");
    const pos = {};  // label -> [r, c]
    for (let r = 0; r < N; r++)
        for (let c = 0; c < N; c++) pos[String.fromCharCode(65+c)+(r+1)] = [r, c];
    for (const st of payload.stones || []) {
        const rc = pos[st.label];
        if (!rc) continue;
        const [r, c] = rc;
        const x = m + c*sp, y = m + r*sp, rr = sp*0.44;
        ctx.beginPath(); ctx.arc(x, y, rr, 0, 7);
        if (st.color === 1) { ctx.fillStyle = "#111114"; ctx.fill();
            ctx.beginPath(); ctx.arc(x-rr*0.2, y-rr*0.25, rr*0.25, 0, 7);
            ctx.fillStyle = "#4a4a52"; ctx.fill();
        } else {
            ctx.fillStyle = "#f7f7f9"; ctx.fill();
            ctx.strokeStyle = "#8d8d92"; ctx.lineWidth = 1; ctx.stroke();
            ctx.beginPath(); ctx.arc(x-rr*0.2, y-rr*0.25, rr*0.25, 0, 7);
            ctx.fillStyle = "#ffffff"; ctx.fill();
        }
    }
    if (payload.awaiting && payload.awaiting.length === 2 && payload.awaiting[0] >= 0) {
        const [r, c] = payload.awaiting;
        const x = m + c*sp, y = m + r*sp;
        ctx.beginPath(); ctx.arc(x, y, sp*0.52, 0, 7);
        ctx.strokeStyle = "#3d8bff"; ctx.lineWidth = 2; ctx.stroke();
    }
    history.push(payload);
    if (history.length > 50) history.shift();
}

function posFromLabel(label) {
    const c = label.charCodeAt(0) - 65;
    const r = parseInt(label.slice(1)) - 1;
    return [r, c];
}

// ---- 日志 ----
function addLog(s) {
    const el = $("log");
    const time = new Date().toTimeString().slice(0, 8);
    el.textContent += time + " " + s + "\n";
    el.scrollTop = el.scrollHeight;
}

// ---- 设置 ----
function currentSettings() {
    return {
        our_color: document.querySelector("#segColor .on").dataset.v,
        engine: $("selEngine").value,
        gpu_device: $("selDevice").value,
        move_delay: parseFloat(fmt1($("selDelay").value)),
        engine_threads: parseInt($("inpThreads").value || "0"),
        think_limit: parseFloat($("selThink").value),
        click_offset_x: parseInt($("inpOffX").value || "0"),
        click_offset_y: parseInt($("inpOffY").value || "0"),
        mate_rush: $("chkMate").checked,
        theme: document.body.dataset.theme,
    };
}

async function applySettings(s) {
    settings = s;
    const cmap = { auto: "自动", "1": "黑", "2": "白" };
    document.querySelectorAll("#segColor button").forEach(b =>
        b.classList.toggle("on", b.dataset.v === (s.our_color || "auto")));
    $("selEngine").value = s.engine || "auto";
    setSelect($("selDelay"), fmt1(s.move_delay ?? 1.0));
    $("inpThreads").value = String(s.engine_threads ?? 0);
    setSelect($("selThink"), String(Math.round(s.think_limit ?? 20)));
    $("inpOffX").value = String(s.click_offset_x ?? 0);
    $("inpOffY").value = String(s.click_offset_y ?? 0);
    $("chkMate").checked = s.mate_rush !== false;
    if (s.theme) setTheme(s.theme, false);
    if (typeof buildEngineList === "function") { try { await buildEngineList(); } catch (e) {} }
    saveHint();
}

function saveHint() {
    const t = parseInt($("inpThreads").value || "0");
    const cpus = navigator.hardwareConcurrency || 0;
    $("threadsHint").textContent = t === 0
        ? `当前线程数：0（用满全部逻辑核${cpus ? "，本机 " + cpus + " 逻辑核" : ""}）`
        : `当前线程数：${t}`;
    const mate = $("chkMate").checked;
    $("engineInfo").textContent = mate
        ? "找到必胜线后快速收尾出招（日志会提示必胜线深度）"
        : "完整必胜证明模式：耗时可能显著超过思考上限，日志会提示";
}

async function save() {
    settings = { ...settings, ...currentSettings() };
    try { await SaveSettings(settings); } catch (e) { addLog("保存设置失败: " + e); }
}

function setTheme(theme, persist) {
    document.body.dataset.theme = theme;
    $("btnTheme").textContent = theme === "dark" ? "浅色" : "深色";
    if (persist) save();
}

// ---- 事件绑定 ----
window.addEventListener("DOMContentLoaded", async () => {
    $("ver").textContent = "v" + await Version();
    try { await applySettings(await GetSettings()); } catch (e) { addLog("读取设置失败: " + e); }
    try { await buildEngineList(); } catch (e) { addLog("引擎探测失败: " + e); }
    setTheme(settings.theme || "dark", false);
    drawEmpty();

    $("btnToggle").onclick = async () => {
        if (!active) {
            try { await StartAuto(); } catch (e) { addLog(String(e)); return; }
        } else { await StopAuto(); }
    };
    $("btnShot").onclick = () => TestShot();
    $("btnDebug").onclick = () => OpenDebugDir();
    $("btnUpdate").onclick = async () => {
        addLog("正在检查更新…");
        const r = await CheckUpdate();
        if (r.has_update)
            alert(`发现新版本：${r.latest}\n当前：v${await Version()}\n前往下载：\n${r.url}`);
        else if (r.error)
            alert("检查更新失败：" + r.error + "\n" + r.url);
        else
            alert(`当前版本 v${await Version()} 已是最新。`);
    };
    $("btnTheme").onclick = () =>
        setTheme(document.body.dataset.theme === "dark" ? "light" : "dark", true);
    document.querySelectorAll("#segColor button").forEach(b =>
        b.onclick = () => {
            document.querySelectorAll("#segColor button").forEach(x => x.classList.remove("on"));
            b.classList.add("on");
            save();
        });
    for (const id of ["selEngine", "selDelay", "selThink", "selDevice"])
        $(id).onchange = () => { updateDeviceVisibility(); save(); };
    $("inpThreads").onchange = save;
    $("chkMate").onchange = save;

    // 后端事件
    EventsOn("board", p => drawBoard(p));
    EventsOn("log", s => addLog(s));
    EventsOn("active", on => {
        active = on;
        const b = $("btnToggle");
        b.textContent = on ? "■  停止自动对弈" : "▶  开始自动对弈";
        b.classList.toggle("accent", !on);
        b.classList.toggle("danger", on);
    });
    EventsOn("status", s => { $("status").textContent = s; });
    EventsOn("our_color", c => { addLog("我方执" + (colorName[c] || "?")); });
    EventsOn("engine", n => {
        $("engineInfo").textContent = "引擎已就绪: " + n +
            (n === "rapfi" ? "（开源强引擎）" : "（内置简易引擎）");
    });
});

