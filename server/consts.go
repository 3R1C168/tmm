package server

// 跨文件复用的字符串常量：避免字面量重复（goconst），统一语义。

// stateSuccess 是登录会话状态机的「成功」终态（auth_code/auth_qr 共用）。
const stateSuccess = "success"

// tmpSuffix 是下载进行中的临时文件后缀（cache/handlers/settings 共用）。
const tmpSuffix = ".tmp"

// actionPause 是任务/文件批量操作的暂停动作标识（handlers/task 共用）。
const actionPause = "pause"

// actionResume 是任务/文件批量操作的恢复动作标识（handlers/task 共用）。
const actionResume = "resume"

// defaultAccount 是未显式指定时的默认账号命名空间。
const defaultAccount = "default"
