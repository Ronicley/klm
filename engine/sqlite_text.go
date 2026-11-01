package main

import (
	"database/sql/driver"
	"fmt"
	"strings"

	"modernc.org/sqlite"
)

func init() {
	// SQLite lower() is ASCII-only. User labels/search must share Go's Unicode
	// case conversion without loading the matching inventory into application RAM.
	if err := sqlite.RegisterDeterministicScalarFunction("klm_lower", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if args[0] == nil {
			return nil, nil
		}
		text, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("klm_lower expects text")
		}
		return strings.ToLower(text), nil
	}); err != nil {
		panic(err)
	}
}
