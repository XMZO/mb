// Package usererr 定义可以直接展示给用户的错误。
//
// 约定:只有 usererr 的文案会原样返回给浏览器;其它错误一律视为内部错误,
// 只写服务端日志,对外显示统一提示,避免泄露 SQL、路径等细节。
package usererr

import (
	"errors"
	"fmt"
)

// Error 是一条面向用户的错误信息。
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

// New 创建一条面向用户的错误。
func New(msg string) error { return &Error{msg: msg} }

// Newf 以格式化方式创建一条面向用户的错误。
func Newf(format string, args ...any) error {
	return &Error{msg: fmt.Sprintf(format, args...)}
}

// Is 判断 err 是否(包裹了)面向用户的错误。
func Is(err error) bool {
	var ue *Error
	return errors.As(err, &ue)
}

// Message 返回 err 中面向用户的文案;err 不是用户错误时返回 fallback。
func Message(err error, fallback string) string {
	var ue *Error
	if errors.As(err, &ue) {
		return ue.msg
	}
	return fallback
}
