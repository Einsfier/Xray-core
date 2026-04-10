package ospf

import (
	"context"
	"fmt"

	"github.com/xtls/xray-core/common/errors"
)

func newError(values ...interface{}) *errors.Error {
	return errors.New(values...)
}

func logDebug(format string, args ...interface{}) {
	errors.LogDebug(context.Background(), fmt.Sprintf(format, args...))
}

func logWarn(format string, args ...interface{}) {
	errors.LogWarning(context.Background(), fmt.Sprintf(format, args...))
}

func logErr(err error, format string, args ...interface{}) {
	errors.LogWarningInner(context.Background(), err, fmt.Sprintf(format, args...))
}
