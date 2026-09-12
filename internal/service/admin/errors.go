package admin

import (
	"errors"
	"fmt"
)

// ValidationError 是 fail-fast 管理面校验失败（携带中文说明，→400/4001）。
// 与运行期 mergeOverrides「非法保留默认」的容错语义刻意区分——管理面即时可见错误，杜绝脏数据入库。
type ValidationError struct {
	msg string
}

func (e *ValidationError) Error() string { return e.msg }

func validationErrorf(format string, args ...any) error {
	return &ValidationError{msg: fmt.Sprintf(format, args...)}
}

var (
	// ErrAlreadyRegistered 已有管理员账号，注册入口已关闭（→403/4031）。
	ErrAlreadyRegistered = errors.New("already registered")
	// ErrInvalidCredentials 登录凭证错误（统一文案不区分「用户不存在/密码错误」，防探测，→401/4011）。
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrWrongOldPassword 修改密码时旧密码不正确（→400/4001）。
	ErrWrongOldPassword = errors.New("wrong old password")
)