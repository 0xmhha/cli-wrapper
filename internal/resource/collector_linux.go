//go:build linux

package resource

var (
	defaultProcessCollector ProcessCollector = nil
	defaultSystemCollector  SystemCollector  = nil
)
