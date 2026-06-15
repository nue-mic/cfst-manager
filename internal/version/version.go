// Package version 暴露构建期可注入的版本信息。
package version

// Number 为版本号，可通过 -ldflags "-X .../internal/version.Number=vX.Y.Z" 注入。
var Number = "dev"

// BuildDate 为构建日期，可经 ldflags 注入。
var BuildDate = "unknown"
