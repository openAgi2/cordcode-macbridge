package gobridge

import "fmt"

const runtimeBinaryName = "cccode-bridge-runtime"

// 通过 -ldflags 在 build 时注入：
//
//	-ldflags "-X github.com/openAgi2/cordcode-macbridge/go-bridge.runtimeVersion=... -X github.com/openAgi2/cordcode-macbridge/go-bridge.runtimeCommit=... -X github.com/openAgi2/cordcode-macbridge/go-bridge.runtimeDate=..."
var (
	runtimeVersion = "0.1.0-dev"
	runtimeCommit  = "unknown"
	runtimeDate    = "unknown"
)

func runtimeVersionString() string {
	return fmt.Sprintf("%s %s (commit: %s, built: %s)", runtimeBinaryName, runtimeVersion, runtimeCommit, runtimeDate)
}
