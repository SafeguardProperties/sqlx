package sqlx

import (
	"database/sql/driver"
	"testing"
)

// Benchmarks in this file use only upstream sqlx API (plus the fake driver)
// so they can be run unchanged against upstream v1.3.5 for comparison.

type benchRec struct {
	ID    int64  `db:"id"`
	A     int64  `db:"a"`
	B     string `db:"b"`
	C     *int64 `db:"c"`
	D     int64  `db:"d"`
	E     string `db:"e"`
	F     *int64 `db:"f"`
	Inner struct {
		G int64 `db:"g"`
	} `db:"inner"`
}

const benchRows = 10000

func setupBench(b *testing.B, q string) *DB {
	rows := make([][]driver.Value, benchRows)
	for i := range rows {
		rows[i] = []driver.Value{int64(i), int64(i), "bee", int64(i), int64(i), "eee", int64(i), int64(i)}
	}
	setFakeResult(q, []string{"id", "a", "b", "c", "d", "e", "f", "inner.g"}, rows...)
	db := openFake(b)
	b.Cleanup(func() { db.Close() })
	b.ReportAllocs()
	b.ResetTimer()
	return db
}

func benchSelect(b *testing.B, db *DB, q string, dest func() interface{}) {
	for i := 0; i < b.N; i++ {
		if err := db.Select(dest(), q); err != nil {
			b.Fatal(err)
		}
	}
}

func benchRowsStructScan(b *testing.B, db *DB, q string, dest interface{}) {
	for i := 0; i < b.N; i++ {
		rows, err := db.Queryx(q)
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			if err := rows.StructScan(dest); err != nil {
				b.Fatal(err)
			}
		}
		rows.Close()
	}
}

func BenchmarkScanSelect(b *testing.B) {
	q := "bench select"
	db := setupBench(b, q)
	benchSelect(b, db, q, func() interface{} { return &[]benchRec{} })
}

func BenchmarkScanRowsStructScan(b *testing.B) {
	q := "bench rows"
	db := setupBench(b, q)
	benchRowsStructScan(b, db, q, &benchRec{})
}
