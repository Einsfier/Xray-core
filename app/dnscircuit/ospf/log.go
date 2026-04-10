package ospf

import (
	"fmt"

	"github.com/xtls/xray-core/common/log"
)

func LogImportant(format string, args ...interface{}) {
	log.Record(&log.PrefixMessage{
		Prefix:  "OSPF",
		Content: fmt.Sprintf(format, args...),
	})
}
