// Package apiresp 提供管理端 API 的统一 JSON 响应助手。
package apiresp

import (
	"encoding/json"
	"net/http"
)

// Envelope 是管理端 API 的统一响应信封（区别于对外兼容 API 的信封）。
type Envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// JSON 写出任意 JSON。
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// OK 写出成功响应。
func OK(w http.ResponseWriter, data any) {
	JSON(w, http.StatusOK, Envelope{OK: true, Data: data})
}

// Created 写出 201。
func Created(w http.ResponseWriter, data any) {
	JSON(w, http.StatusCreated, Envelope{OK: true, Data: data})
}

// Err 写出错误响应。
func Err(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, Envelope{OK: false, Error: msg})
}
