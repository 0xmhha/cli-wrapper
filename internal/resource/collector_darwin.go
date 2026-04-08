//go:build darwin

package resource

var (
	defaultProcessCollector ProcessCollector = nil
	defaultSystemCollector  SystemCollector  = nil
)
