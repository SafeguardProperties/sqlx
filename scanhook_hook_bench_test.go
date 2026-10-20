package sqlx

import (
	"database/sql"
	"reflect"
	"testing"
)

// benchStampRec is benchRec with one *int64 field replaced by a registered
// *stamp field.
type benchStampRec struct {
	ID    int64  `db:"id"`
	A     int64  `db:"a"`
	B     string `db:"b"`
	C     *int64 `db:"c"`
	D     int64  `db:"d"`
	E     string `db:"e"`
	F     *stamp `db:"f"`
	Inner struct {
		G int64 `db:"g"`
	} `db:"inner"`
}

func registerUnrelated(b *testing.B) {
	typ := reflect.TypeOf((**complex128)(nil))
	RegisterScanner(typ, func(ptr interface{}) sql.Scanner { return nil })
	b.Cleanup(func() { RegisterScanner(typ, nil) })
}

func registerStampBench(b *testing.B) {
	RegisterScanner(stampPtrPtr, func(ptr interface{}) sql.Scanner {
		return stampScanner{p: ptr.(**stamp)}
	})
	b.Cleanup(func() { RegisterScanner(stampPtrPtr, nil) })
}

func BenchmarkScanSelectUnrelatedRegistered(b *testing.B) {
	registerUnrelated(b)
	q := "bench select unrelated"
	db := setupBench(b, q)
	benchSelect(b, db, q, func() interface{} { return &[]benchRec{} })
}

func BenchmarkScanSelectRegisteredField(b *testing.B) {
	registerStampBench(b)
	q := "bench select registered"
	db := setupBench(b, q)
	benchSelect(b, db, q, func() interface{} { return &[]benchStampRec{} })
}

func BenchmarkScanRowsStructScanUnrelatedRegistered(b *testing.B) {
	registerUnrelated(b)
	q := "bench rows unrelated"
	db := setupBench(b, q)
	benchRowsStructScan(b, db, q, &benchRec{})
}

func BenchmarkScanRowsStructScanRegisteredField(b *testing.B) {
	registerStampBench(b)
	q := "bench rows registered"
	db := setupBench(b, q)
	benchRowsStructScan(b, db, q, &benchStampRec{})
}
