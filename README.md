# tg-session-checker-go

一个独立的 Go 小工具，用来批量筛 Telegram `Telethon .session` 账号状态。

当前功能：

- 支持输入目录、单个 `.session`、或一个包含多个 `.session` 的 `.zip`
- 支持三种检查模式：`alive` / `spam` / `both`
- 自动把 Telethon sqlite session 转成 gotd 可读格式
- 可选是否向 `@SpamBot` 发起检查
- 输出 `report.csv`、`report.json`
- 自动打包 `normal_sessions.zip` 和 `abnormal_sessions.zip`

## 新设备依赖

如果你用我打包好的 Windows 版并直接运行 `tg-session-checker-go.exe` 或 `start.bat`：

- 不需要额外依赖

如果你要自己源码启动：

- 安装 `Go 1.24+`

不用装 Python，不用装 Telethon，不用单独装 sqlite。

## 交互启动

先给脚本执行权限：

```bash
chmod +x start.sh
```

然后直接运行：

```bash
./start.sh
```

它会在终端里让你依次选择：

1. 检查模式
2. 输入路径
3. 输出目录

脚本默认：

- `TG_APP_ID=2040`
- `TG_APP_HASH=b18441a1ff607e10a989891a5462e627`
- `MODE=alive`
- `OUTPUT_DIR=./output`

## 也可以直接传路径

```bash
./start.sh /path/to/session_or_zip
```

这时默认还是 `alive` 模式；如果你想换模式，可以先带环境变量：

```bash
MODE=both ./start.sh /path/to/session_or_zip
```

## Windows 启动

如果你在 Windows 上用我打包好的文件，直接在终端里运行：

```bat
start.bat
```

它会自动跳到 `start.ps1` 交互界面。

如果目录里已经带了 `tg-session-checker-go.exe`，它会优先直接跑 exe，不需要你自己装 Go。

## 手动命令行启动

```bash
go run . -input /path/to/session_or_zip -mode alive -out ./output
```

常用参数：

- `-mode alive` 只筛活号，不走 `@SpamBot`
- `-mode spam` 只看 `@SpamBot` 限制状态
- `-mode both` 先验证登录，再跑 `@SpamBot`
- `-workers 5` 并发检查数
- `-timeout 45s` 单账号超时

## 输出说明

- `report.csv`：完整检查结果
- `report.json`：JSON 版本结果
- `normal_sessions.zip`：判定为正常无异常的账号
- `abnormal_sessions.zip`：限制、封禁、失败、未知状态账号

## 状态含义

- `alive`：session 有效，账号能正常登录
- `active`：正常无限制
- `restricted`：临时限制或双向限制
- `spam`：垃圾消息风控
- `banned`：永久封禁
- `frozen`：等待验证 / 冻结
- `unauthorized`：session 失效或未授权
- `failed`：检查过程失败
- `unknown`：拿到回复但没识别出来
