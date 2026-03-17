package models

// Response 通用响应结构
type Response struct {
	Code   int         `json:"code"`
	Result interface{} `json:"result"`
	Msg    string      `json:"msg"`
}

// NewResponse 创建响应
func NewResponse(code int, result interface{}, msg string) Response {
	return Response{
		Code:   code,
		Result: result,
		Msg:    msg,
	}
}

// Success 成功响应
func Success(result interface{}) Response {
	return NewResponse(0, result, "成功")
}

// Error 错误响应
func Error(msg string) Response {
	return NewResponse(-1, nil, msg)
}
