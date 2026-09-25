// 领域层公共错误哨兵：定义在 entity 共享层，供全部 infra 与 service 层引用。
// 运行期领域哨兵（ErrNotFound 等）与管理面校验失败错误（ValidationError 等）集中于此。
package entity

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound 表示请求的资源不存在（记录未找到、文件缺失等）。
	ErrNotFound = errors.New("not found")

	// ErrConflict 表示唯一约束或状态冲突（重复插入、乐观锁冲突等）。
	ErrConflict = errors.New("conflict")

	// ErrClosed 表示资源已关闭、连接已断开。
	ErrClosed = errors.New("closed")

	// ErrRateLimited 表示消息在限流中间件等待令牌超时，已被丢弃。
	// 由连接层识别后回复固定文案，其余层无需处理。
	ErrRateLimited = errors.New("rate limited")

	// ErrSensitiveWord 表示消息命中敏感词，已被拦截丢弃。
	// 由连接层识别后回复固定文案；命中词见 SensitiveWordError.Word。
	ErrSensitiveWord = errors.New("sensitive word")

	// ErrAlreadyRegistered 已有管理员账号，注册入口已关闭（→403/4031）。
	ErrAlreadyRegistered = errors.New("already registered")
	// ErrInvalidCredentials 登录凭证错误（统一文案不区分「用户不存在/密码错误」，防探测，→401/4011）。
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrWrongOldPassword 修改密码时旧密码不正确（→400/4001）。
	ErrWrongOldPassword = errors.New("wrong old password")
)

// SensitiveWordError 是携带命中词的敏感词拦截错误。
// 通过 errors.Is(err, ErrSensitiveWord) 判断类型，errors.As 取命中词。
type SensitiveWordError struct {
	Word string // 命中的敏感词（保留配置中的原始大小写，便于日志）
}

func (e *SensitiveWordError) Error() string {
	return "sensitive word: " + e.Word
}

// Unwrap 使 errors.Is 能匹配到 ErrSensitiveWord 哨兵。
func (e *SensitiveWordError) Unwrap() error {
	return ErrSensitiveWord
}

// ValidationError 是 fail-fast 管理面校验失败（携带中文说明，→400/4001）。
// 与运行期 mergeOverrides「非法保留默认」的容错语义刻意区分——管理面即时可见错误，杜绝脏数据入库。
type ValidationError struct {
	msg string
}

func (e *ValidationError) Error() string { return e.msg }

// ValidationErrorf 构造 ValidationError（service/admin 校验失败时返回，
// handler/web 经 errors.As 识别 → 400/4001）。
func ValidationErrorf(format string, args ...any) error {
	return &ValidationError{msg: fmt.Sprintf(format, args...)}
}