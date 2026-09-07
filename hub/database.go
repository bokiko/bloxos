package main

// databaseDSN returns the hub's SQLite (modernc.org/sqlite) DSN for path.
// WAL keeps readers and the writer concurrent; busy_timeout(5000) covers
// ordinary lock waits; foreign_keys(1) keeps cascade deletes active.
// _txlock=immediate makes explicit transactions take the write lock at
// BEGIN: a deferred transaction that reads then upgrades on its first write
// can fail immediately with SQLITE_BUSY (the busy handler does not cover
// the upgrade), which surfaced as retried power-history commit failures
// under live load. Pool size stays at the database/sql default: readers
// remain concurrent, only writers serialize.
func databaseDSN(path string) string {
	return path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
}
