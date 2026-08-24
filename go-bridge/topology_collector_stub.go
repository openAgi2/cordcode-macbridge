//go:build !darwin

package gobridge

// topology_collector_stub.go —— 非 darwin 平台：全部维度 not_implemented（P2-3）。
// 不启动任何循环命令、不报 split；行为定义在公共层 stubCollector，本文仅作平台接线。

func newPlatformCollector() TopologyCollector {
	return newStubCollector()
}
