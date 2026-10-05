// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sql 提供了围绕 SQL（或类 SQL）数据库的通用接口。
//
// sql 包必须与数据库驱动一起使用。
// 驱动列表见 https://golang.org/s/sqldrivers。
//
// 不支持 context 取消的驱动在查询完成之前不会返回。
//
// 用法示例见 wiki 页面 https://golang.org/s/sqlwiki。
package sql

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	_ "unsafe"
)

var driversMu sync.RWMutex

// drivers 本应是一个内部细节，
// 但被广泛使用的包通过 linkname 访问它。
// （它们 linkname 了 drivers 却没有 linkname driversMu，这尤其错误。）
// 耻辱柱上值得注意的成员包括：
//   - github.com/instana/go-sensor
//
// 不要删除或更改类型签名。
// 参见 go.dev/issue/67401。
//
//go:linkname drivers
var drivers = make(map[string]driver.Driver)

// nowFunc 返回当前时间；它在测试中会被覆盖。
var nowFunc = time.Now

// Register 使数据库驱动可以通过提供的名称使用。
// 如果以相同名称调用 Register 两次，或者 driver 为 nil，
// 它会 panic。
func Register(name string, driver driver.Driver) {
	driversMu.Lock()
	defer driversMu.Unlock()
	if driver == nil {
		panic("sql: Register driver is nil")
	}
	if _, dup := drivers[name]; dup {
		panic("sql: Register called twice for driver " + name)
	}
	drivers[name] = driver
}

func unregisterAllDrivers() {
	driversMu.Lock()
	defer driversMu.Unlock()
	// 用于测试。
	drivers = make(map[string]driver.Driver)
}

// Drivers 返回已注册驱动名称的排序列表。
func Drivers() []string {
	driversMu.RLock()
	defer driversMu.RUnlock()
	return slices.Sorted(maps.Keys(drivers))
}

// NamedArg 是一个命名参数。NamedArg 值可用作
// [DB.Query] 或 [DB.Exec] 的参数，并绑定到 SQL 语句中
// 对应的命名参数。
//
// 有关创建 NamedArg 值的更简洁方式，见
// [Named] 函数。
type NamedArg struct {
	_NamedFieldsRequired struct{}

	// Name 是参数占位符的名称。
	//
	// 如果为空，将使用参数列表中的
	// 序号位置。
	//
	// Name 必须省略任何符号前缀。
	Name string

	// Value 是参数的值。
	// 它可以被赋予与查询参数
	// 相同的值类型。
	Value any
}

// Named 提供了创建 [NamedArg] 值的更简洁方式。
//
// 用法示例：
//
//	db.ExecContext(ctx, `
//	    delete from Invoice
//	    where
//	        TimeCreated < @end
//	        and TimeCreated >= @start;`,
//	    sql.Named("start", startTime),
//	    sql.Named("end", endTime),
//	)
func Named(name string, value any) NamedArg {
	// 存在这个函数是因为 go1compat 承诺
	// 并不保证结构体不会增加更多字段，
	// 因此无键结构体字面量是一个 vet 错误。所以我们
	// 不希望允许 sql.NamedArg{name, value}。
	return NamedArg{Name: name, Value: value}
}

// IsolationLevel 是 [TxOptions] 中使用的事务隔离级别。
type IsolationLevel int

// 驱动可以在 [DB.BeginTx] 中支持的各种隔离级别。
// 如果驱动不支持给定的隔离级别，可能会返回错误。
//
// 参见 https://en.wikipedia.org/wiki/Isolation_(database_systems)#Isolation_levels。
const (
	LevelDefault IsolationLevel = iota
	LevelReadUncommitted
	LevelReadCommitted
	LevelWriteCommitted
	LevelRepeatableRead
	LevelSnapshot
	LevelSerializable
	LevelLinearizable
)

// String 返回事务隔离级别的名称。
func (i IsolationLevel) String() string {
	switch i {
	case LevelDefault:
		return "Default"
	case LevelReadUncommitted:
		return "Read Uncommitted"
	case LevelReadCommitted:
		return "Read Committed"
	case LevelWriteCommitted:
		return "Write Committed"
	case LevelRepeatableRead:
		return "Repeatable Read"
	case LevelSnapshot:
		return "Snapshot"
	case LevelSerializable:
		return "Serializable"
	case LevelLinearizable:
		return "Linearizable"
	default:
		return "IsolationLevel(" + strconv.Itoa(int(i)) + ")"
	}
}

var _ fmt.Stringer = LevelDefault

// TxOptions 保存 [DB.BeginTx] 中使用的事务选项。
type TxOptions struct {
	// Isolation 是事务隔离级别。
	// 如果为零，则使用驱动或数据库的默认级别。
	Isolation IsolationLevel
	ReadOnly  bool
}

// RawBytes 是一个字节切片，持有对数据库自身拥有的内存的引用。
// 将 [Rows.Scan] 扫描到 RawBytes 之后，该切片仅在
// 下次调用 [Rows.Next]、[Rows.Scan] 或 [Rows.Close] 之前有效。
type RawBytes []byte

// NullString 表示一个可能为 null 的字符串。
// NullString 实现了 [Scanner] 接口，因此
// 它可以用作扫描目标：
//
//	var s NullString
//	err := db.QueryRow("SELECT name FROM foo WHERE id=?", id).Scan(&s)
//	...
//	if s.Valid {
//	   // 使用 s.String
//	} else {
//	   // NULL 值
//	}
type NullString struct {
	String string
	Valid  bool // 如果 String 不为 NULL，则 Valid 为 true
}

// Scan 实现了 [Scanner] 接口。
func (ns *NullString) Scan(value any) error {
	if value == nil {
		ns.String, ns.Valid = "", false
		return nil
	}
	ns.Valid = true
	return convertAssign(&ns.String, value)
}

// Value 实现了 [driver.Valuer] 接口。
func (ns NullString) Value() (driver.Value, error) {
	if !ns.Valid {
		return nil, nil
	}
	return ns.String, nil
}

// NullInt64 表示一个可能为 null 的 int64。
// NullInt64 实现了 [Scanner] 接口，因此
// 它可以用作扫描目标，类似于 [NullString]。
type NullInt64 struct {
	Int64 int64
	Valid bool // 如果 Int64 不为 NULL，则 Valid 为 true
}

// Scan 实现了 [Scanner] 接口。
func (n *NullInt64) Scan(value any) error {
	if value == nil {
		n.Int64, n.Valid = 0, false
		return nil
	}
	n.Valid = true
	return convertAssign(&n.Int64, value)
}

// Value 实现了 [driver.Valuer] 接口。
func (n NullInt64) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Int64, nil
}

// NullInt32 表示一个可能为 null 的 int32。
// NullInt32 实现了 [Scanner] 接口，因此
// 它可以用作扫描目标，类似于 [NullString]。
type NullInt32 struct {
	Int32 int32
	Valid bool // 如果 Int32 不为 NULL，则 Valid 为 true
}

// Scan 实现了 [Scanner] 接口。
func (n *NullInt32) Scan(value any) error {
	if value == nil {
		n.Int32, n.Valid = 0, false
		return nil
	}
	n.Valid = true
	return convertAssign(&n.Int32, value)
}

// Value 实现了 [driver.Valuer] 接口。
func (n NullInt32) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return int64(n.Int32), nil
}

// NullInt16 表示一个可能为 null 的 int16。
// NullInt16 实现了 [Scanner] 接口，因此
// 它可以用作扫描目标，类似于 [NullString]。
type NullInt16 struct {
	Int16 int16
	Valid bool // 如果 Int16 不为 NULL，则 Valid 为 true
}

// Scan 实现了 [Scanner] 接口。
func (n *NullInt16) Scan(value any) error {
	if value == nil {
		n.Int16, n.Valid = 0, false
		return nil
	}
	err := convertAssign(&n.Int16, value)
	n.Valid = err == nil
	return err
}

// Value 实现了 [driver.Valuer] 接口。
func (n NullInt16) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return int64(n.Int16), nil
}

// NullByte 表示一个可能为 null 的 byte。
// NullByte 实现了 [Scanner] 接口，因此
// 它可以用作扫描目标，类似于 [NullString]。
type NullByte struct {
	Byte  byte
	Valid bool // 如果 Byte 不为 NULL，则 Valid 为 true
}

// Scan 实现了 [Scanner] 接口。
func (n *NullByte) Scan(value any) error {
	if value == nil {
		n.Byte, n.Valid = 0, false
		return nil
	}
	err := convertAssign(&n.Byte, value)
	n.Valid = err == nil
	return err
}

// Value 实现了 [driver.Valuer] 接口。
func (n NullByte) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return int64(n.Byte), nil
}

// NullFloat64 表示一个可能为 null 的 float64。
// NullFloat64 实现了 [Scanner] 接口，因此
// 它可以用作扫描目标，类似于 [NullString]。
type NullFloat64 struct {
	Float64 float64
	Valid   bool // 如果 Float64 不为 NULL，则 Valid 为 true
}

// Scan 实现了 [Scanner] 接口。
func (n *NullFloat64) Scan(value any) error {
	if value == nil {
		n.Float64, n.Valid = 0, false
		return nil
	}
	n.Valid = true
	return convertAssign(&n.Float64, value)
}

// Value 实现了 [driver.Valuer] 接口。
func (n NullFloat64) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Float64, nil
}

// NullBool 表示一个可能为 null 的 bool。
// NullBool 实现了 [Scanner] 接口，因此
// 它可以用作扫描目标，类似于 [NullString]。
type NullBool struct {
	Bool  bool
	Valid bool // 如果 Bool 不为 NULL，则 Valid 为 true
}

// Scan 实现了 [Scanner] 接口。
func (n *NullBool) Scan(value any) error {
	if value == nil {
		n.Bool, n.Valid = false, false
		return nil
	}
	n.Valid = true
	return convertAssign(&n.Bool, value)
}

// Value 实现了 [driver.Valuer] 接口。
func (n NullBool) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Bool, nil
}

// NullTime 表示一个可能为 null 的 [time.Time]。
// NullTime 实现了 [Scanner] 接口，因此
// 它可以用作扫描目标，类似于 [NullString]。
type NullTime struct {
	Time  time.Time
	Valid bool // 如果 Time 不为 NULL，则 Valid 为 true
}

// Scan 实现了 [Scanner] 接口。
func (n *NullTime) Scan(value any) error {
	if value == nil {
		n.Time, n.Valid = time.Time{}, false
		return nil
	}
	n.Valid = true
	return convertAssign(&n.Time, value)
}

// Value 实现了 [driver.Valuer] 接口。
func (n NullTime) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Time, nil
}

// Null 表示一个可能为 null 的值。
// Null 实现了 [Scanner] 接口，因此
// 它可以用作扫描目标：
//
//	var s Null[string]
//	err := db.QueryRow("SELECT name FROM foo WHERE id=?", id).Scan(&s)
//	...
//	if s.Valid {
//	   // 使用 s.V
//	} else {
//	   // NULL 值
//	}
//
// T 应为 [driver.Value] 所接受的类型之一。
type Null[T any] struct {
	V     T
	Valid bool
}

func (n *Null[T]) Scan(value any) error {
	if value == nil {
		n.V, n.Valid = *new(T), false
		return nil
	}
	n.Valid = true
	return convertAssign(&n.V, value)
}

func (n Null[T]) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	v := any(n.V)
	// 见 issue 69728。
	if valuer, ok := v.(driver.Valuer); ok {
		val, err := callValuerValue(valuer)
		if err != nil {
			return val, err
		}
		v = val
	}
	// 见 issue 69837。
	return driver.DefaultParameterConverter.ConvertValue(v)
}

// Scanner 是 [Rows.Scan] 使用的接口。
type Scanner interface {
	// Scan 从数据库驱动赋值一个值。
	//
	// src 值将是以下类型之一：
	//
	//    int64
	//    float64
	//    bool
	//    []byte
	//    string
	//    time.Time
	//    nil - 表示 NULL 值
	//
	// 如果值无法在不丢失信息的情况下被存储，
	// 则应返回错误。
	//
	// []byte 之类的引用类型仅在下次调用 Scan 之前有效，
	// 不应被保留。它们的底层内存归驱动所有。
	// 如果必须保留，请在下次调用 Scan 之前复制其值。
	Scan(src any) error
}

// Out 可用于从存储过程检索 OUTPUT 值参数。
//
// 并非所有驱动和数据库都支持 OUTPUT 值参数。
//
// 用法示例：
//
//	var outArg string
//	_, err := db.ExecContext(ctx, "ProcName", sql.Named("Arg1", sql.Out{Dest: &outArg}))
type Out struct {
	_NamedFieldsRequired struct{}

	// Dest 是一个指针，指向将被设置为存储过程
	// OUTPUT 参数结果的值。
	Dest any

	// In 表示参数是否为 INOUT 参数。如果是，存储过程的
	// 输入值是 Dest 指针解引用后的值，随后该值会被
	// 输出值替换。
	In bool
}

// ErrNoRows 由 [Row.Scan] 在 [DB.QueryRow] 未返回行时返回。
// 在这种情况下，QueryRow 返回一个占位的 [*Row] 值，
// 将该错误推迟到 Scan 时才产生。
var ErrNoRows = errors.New("sql: no rows in result set")

// DB 是一个数据库句柄，表示零个或多个底层连接的池。
// 它可以被多个 goroutine 并发安全地使用。
//
// sql 包会自动创建和释放连接；它
// 还维护一个空闲连接池。如果数据库有
// 每连接状态的概念，这种状态可以在事务（[Tx]）
// 或连接（[Conn]）中被可靠地观察到。一旦调用 [DB.Begin]，
// 返回的 [Tx] 就绑定到一个单一连接。一旦对该事务
// 调用 [Tx.Commit] 或 [Tx.Rollback]，该事务的连接
// 就会被归还到 [DB] 的空闲连接池。池的大小
// 可以通过 [DB.SetMaxIdleConns] 控制。
type DB struct {
	// 等待新连接的总时间。
	waitDuration atomic.Int64

	connector driver.Connector
	// numClosed 是一个原子计数器，表示已关闭连接的
	// 总数。Stmt.openStmt 在清理 Stmt.css 中的
	// 已关闭连接之前会检查它。
	numClosed atomic.Uint64

	mu           sync.Mutex    // 保护以下字段
	freeConn     []*driverConn // 空闲连接，按 returnedAt 从旧到新排序
	connRequests connRequestSet
	numOpen      int // 已打开和待打开的连接数
	// 用于发出需要新连接的信号。
	// 运行 connectionOpener() 的 goroutine 在此 chan 上读取，
	// maybeOpenNewConnections 在此 chan 上发送（每个所需连接发送一次）。
	// 它在 db.Close() 期间被关闭。关闭操作会通知 connectionOpener
	// goroutine 退出。
	openerCh          chan struct{}
	closed            bool
	dep               map[finalCloser]depSet
	lastPut           map[*driverConn]string // 上次 put conn 的堆栈跟踪；仅用于调试
	maxIdleCount      int                    // 零表示 defaultMaxIdleConns；负数表示 0
	maxOpen           int                    // <= 0 表示无限制
	maxLifetime       time.Duration          // 连接可被复用的最长时间
	maxIdleTime       time.Duration          // 连接在被关闭前可处于空闲状态的最长时间
	cleanerCh         chan struct{}
	waitCount         int64 // 等待过的连接总数。
	maxIdleClosed     int64 // 因空闲数量而被关闭的连接总数。
	maxIdleTimeClosed int64 // 因空闲时间而被关闭的连接总数。
	maxLifetimeClosed int64 // 因最大连接生命周期限制而被关闭的连接总数。

	stop func() // stop 取消连接打开器。
}

// connReuseStrategy 决定 (*DB).conn 如何返回数据库连接。
type connReuseStrategy uint8

const (
	// alwaysNewConn 强制与数据库建立新连接。
	alwaysNewConn connReuseStrategy = iota
	// cachedOrNewConn 返回缓存的连接（如果有），否则等待
	// 一个连接变为可用（如果已达到 MaxOpenConns），或
	// 创建一个新的数据库连接。
	cachedOrNewConn
)

// driverConn 用互斥锁包装 driver.Conn，
// 在进入 Conn 的所有调用期间持有该锁（包括对通过该 Conn
// 返回的接口的任何调用，例如对 Tx、Stmt、
// Result、Rows 的调用）。
type driverConn struct {
	db        *DB
	createdAt time.Time

	sync.Mutex  // 保护以下字段
	ci          driver.Conn
	needReset   bool // 如果为 true，连接会话应在使用前重置。
	closed      bool
	finalClosed bool // ci.Close 已被调用
	openStmt    map[*driverStmt]bool

	// 由 db.mu 保护
	inUse      bool
	dbmuClosed bool      // 与 closed 相同，但由 db.mu 保护，用于 removeClosedStmtLocked
	returnedAt time.Time // 连接被创建或归还的时间。
	onPut      []func()  // 下次归还 conn 时运行的代码（持有 db.mu）
}

func (dc *driverConn) releaseConn(err error) {
	dc.db.putConn(dc, err, true)
}

func (dc *driverConn) removeOpenStmt(ds *driverStmt) {
	dc.Lock()
	defer dc.Unlock()
	delete(dc.openStmt, ds)
}

func (dc *driverConn) expired(timeout time.Duration) bool {
	if timeout <= 0 {
		return false
	}
	return dc.createdAt.Add(timeout).Before(nowFunc())
}

// resetSession 检查驱动连接是否需要重置会话，
// 如果需要则进行重置。
func (dc *driverConn) resetSession(ctx context.Context) error {
	dc.Lock()
	defer dc.Unlock()

	if !dc.needReset {
		return nil
	}
	if cr, ok := dc.ci.(driver.SessionResetter); ok {
		return cr.ResetSession(ctx)
	}
	return nil
}

// validateConnection 检查连接是否有效并且
// 仍可使用。它还在需要时将会话标记为需要重置。
func (dc *driverConn) validateConnection(needsReset bool) bool {
	dc.Lock()
	defer dc.Unlock()

	if needsReset {
		dc.needReset = true
	}
	if cv, ok := dc.ci.(driver.Validator); ok {
		return cv.IsValid()
	}
	return true
}

// prepareLocked 在 dc 上准备查询。当 cg == nil 时，dc 必须在
// 池中跟踪已准备的语句。
func (dc *driverConn) prepareLocked(ctx context.Context, cg stmtConnGrabber, query string) (*driverStmt, error) {
	si, err := ctxDriverPrepare(ctx, dc.ci, query)
	if err != nil {
		return nil, err
	}
	ds := &driverStmt{Locker: dc, si: si}

	// 如果只有一个连接获取器，则无需管理已打开的语句。
	if cg != nil {
		return ds, nil
	}

	// 跟踪每个 driverConn 已打开的语句，以便我们可以在关闭
	// 连接之前关闭它们。
	//
	// 将所有 driver.Stmt 包装为 *driverStmt，以确保它们只被关闭一次。
	if dc.openStmt == nil {
		dc.openStmt = make(map[*driverStmt]bool)
	}
	dc.openStmt[ds] = true
	return ds, nil
}

// 持有 dc.db 的 Mutex。
func (dc *driverConn) closeDBLocked() func() error {
	dc.Lock()
	defer dc.Unlock()
	if dc.closed {
		return func() error { return errors.New("sql: duplicate driverConn close") }
	}
	dc.closed = true
	return dc.db.removeDepLocked(dc, dc)
}

func (dc *driverConn) Close() error {
	dc.Lock()
	if dc.closed {
		dc.Unlock()
		return errors.New("sql: duplicate driverConn close")
	}
	dc.closed = true
	dc.Unlock() // 不是 defer；removeDep finalClose 调用可能需要加锁

	// 现在进行需要持有 dc.mu.Lock 的更新。
	dc.db.mu.Lock()
	dc.dbmuClosed = true
	fn := dc.db.removeDepLocked(dc, dc)
	dc.db.mu.Unlock()
	return fn()
}

func (dc *driverConn) finalClose() error {
	var err error

	// 每个 *driverStmt 都持有 dc 的锁。在对每个 stmt
	// 调用 close 之前，先将列表从 dc 中复制出来。
	var openStmt []*driverStmt
	withLock(dc, func() {
		openStmt = make([]*driverStmt, 0, len(dc.openStmt))
		for ds := range dc.openStmt {
			openStmt = append(openStmt, ds)
		}
		dc.openStmt = nil
	})
	for _, ds := range openStmt {
		ds.Close()
	}
	withLock(dc, func() {
		dc.finalClosed = true
		err = dc.ci.Close()
		dc.ci = nil
	})

	dc.db.mu.Lock()
	dc.db.numOpen--
	dc.db.maybeOpenNewConnections()
	dc.db.mu.Unlock()

	dc.db.numClosed.Add(1)
	return err
}

// driverStmt 将 driver.Stmt 与它来自的 *driverConn
// 关联起来，这样在调用期间可以持有
// driverConn 的锁。
type driverStmt struct {
	sync.Locker // 即 *driverConn
	si          driver.Stmt
	closed      bool
	closeErr    error // 上次 Close 调用的返回值
}

// Close 确保 driver.Stmt 只被关闭一次，并且始终返回
// 相同的结果。
func (ds *driverStmt) Close() error {
	ds.Lock()
	defer ds.Unlock()
	if ds.closed {
		return ds.closeErr
	}
	ds.closed = true
	ds.closeErr = ds.si.Close()
	return ds.closeErr
}

// depSet 是某个 finalCloser 尚未完成的依赖
type depSet map[any]bool // true 布尔值的集合

// finalCloser 接口由 (*DB).addDep 及相关的
// 依赖引用计数使用。
type finalCloser interface {
	// 当某个对象的引用计数降为零时，会调用 finalClose。
	// 调用时不会持有 (*DB).mu。
	finalClose() error
}

// addDep 记录 x 现在依赖于 dep，在 x 的所有依赖都通过 removeDep
// 被移除之前，不会调用 x 的 finalClose。
func (db *DB) addDep(x finalCloser, dep any) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.addDepLocked(x, dep)
}

func (db *DB) addDepLocked(x finalCloser, dep any) {
	if db.dep == nil {
		db.dep = make(map[finalCloser]depSet)
	}
	xdep := db.dep[x]
	if xdep == nil {
		xdep = make(depSet)
		db.dep[x] = xdep
	}
	xdep[dep] = true
}

// removeDep 记录 x 不再依赖于 dep。
// 如果 x 仍有依赖，返回 nil。
// 如果 x 不再有任何依赖，将调用其 finalClose 方法
// 并返回其错误值。
func (db *DB) removeDep(x finalCloser, dep any) error {
	db.mu.Lock()
	fn := db.removeDepLocked(x, dep)
	db.mu.Unlock()
	return fn()
}

func (db *DB) removeDepLocked(x finalCloser, dep any) func() error {
	xdep, ok := db.dep[x]
	if !ok {
		panic(fmt.Sprintf("unpaired removeDep: no deps for %T", x))
	}

	l0 := len(xdep)
	delete(xdep, dep)

	switch len(xdep) {
	case l0:
		// 没有移除任何内容。不应该发生。
		panic(fmt.Sprintf("unpaired removeDep: no %T dep on %T", dep, x))
	case 0:
		// 没有更多依赖。
		delete(db.dep, x)
		return x.finalClose
	default:
		// 仍有依赖。
		return func() error { return nil }
	}
}

// 这是 connectionOpener 请求 chan（DB.openerCh）的大小。
// 该值应当大于 DB.maxOpen 使用的典型最大值。
// 如果 maxOpen 显著大于 connectionRequestQueueSize，
// 那么进入 *DB 的所有调用都可能阻塞，直到 connectionOpener
// 能够满足积压的请求为止。
var connectionRequestQueueSize = 1000000

type dsnConnector struct {
	dsn    string
	driver driver.Driver
}

func (t dsnConnector) Connect(_ context.Context) (driver.Conn, error) {
	return t.driver.Open(t.dsn)
}

func (t dsnConnector) Driver() driver.Driver {
	return t.driver
}

// OpenDB 使用 [driver.Connector] 打开数据库，允许驱动
// 绕过基于字符串的数据源名称。
//
// 大多数用户会通过返回 [*DB] 的驱动特定连接辅助函数
// 来打开数据库。Go 标准库中不包含任何数据库驱动。
// 第三方驱动列表见 https://golang.org/s/sqldrivers。
//
// OpenDB 可能只验证其参数而不创建到数据库的连接。
// 要验证数据源名称是否有效，请调用
// [DB.Ping]。
//
// 返回的 [DB] 可以被多个 goroutine 并发安全地使用，
// 并维护自己的空闲连接池。因此，OpenDB
// 函数应当只被调用一次。很少需要
// 关闭 [DB]。
func OpenDB(c driver.Connector) *DB {
	ctx, cancel := context.WithCancel(context.Background())
	db := &DB{
		connector: c,
		openerCh:  make(chan struct{}, connectionRequestQueueSize),
		lastPut:   make(map[*driverConn]string),
		stop:      cancel,
	}

	go db.connectionOpener(ctx)

	return db
}

// Open 打开由其数据库驱动名称和驱动特定的数据源名称
// 指定的数据库，数据源名称通常至少包含
// 数据库名称和连接信息。
//
// 大多数用户会通过返回 [*DB] 的驱动特定连接辅助函数
// 来打开数据库。Go 标准库中不包含任何数据库驱动。
// 第三方驱动列表见 https://golang.org/s/sqldrivers。
//
// Open 可能只验证其参数而不创建到数据库的连接。
// 要验证数据源名称是否有效，请调用
// [DB.Ping]。
//
// 返回的 [DB] 可以被多个 goroutine 并发安全地使用，
// 并维护自己的空闲连接池。因此，Open
// 函数应当只被调用一次。很少需要
// 关闭 [DB]。
func Open(driverName, dataSourceName string) (*DB, error) {
	driversMu.RLock()
	driveri, ok := drivers[driverName]
	driversMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("sql: unknown driver %q (forgotten import?)", driverName)
	}

	if driverCtx, ok := driveri.(driver.DriverContext); ok {
		connector, err := driverCtx.OpenConnector(dataSourceName)
		if err != nil {
			return nil, err
		}
		return OpenDB(connector), nil
	}

	return OpenDB(dsnConnector{dsn: dataSourceName, driver: driveri}), nil
}

func (db *DB) pingDC(ctx context.Context, dc *driverConn, release func(error)) error {
	var err error
	if pinger, ok := dc.ci.(driver.Pinger); ok {
		withLock(dc, func() {
			err = pinger.Ping(ctx)
		})
	}
	release(err)
	return err
}

// PingContext 验证到数据库的连接仍然有效，
// 必要时会建立连接。
func (db *DB) PingContext(ctx context.Context) error {
	var dc *driverConn
	var err error

	err = db.retry(func(strategy connReuseStrategy) error {
		dc, err = db.conn(ctx, strategy)
		return err
	})

	if err != nil {
		return err
	}

	return db.pingDC(ctx, dc, dc.releaseConn)
}

// Ping 验证到数据库的连接仍然有效，
// 必要时会建立连接。
//
// Ping 内部使用 [context.Background]；要指定 context，请使用
// [DB.PingContext]。
func (db *DB) Ping() error {
	return db.PingContext(context.Background())
}

// Close 关闭数据库并阻止新查询开始。
// 然后 Close 会等待所有已在服务器上开始处理的查询
// 完成。
//
// 很少调用 Close 关闭 [DB]，因为 [DB] 句柄应当是
// 长期存在并在许多 goroutine 之间共享的。
func (db *DB) Close() error {
	db.mu.Lock()
	if db.closed { // 使 DB.Close 幂等
		db.mu.Unlock()
		return nil
	}
	if db.cleanerCh != nil {
		close(db.cleanerCh)
	}
	var err error
	fns := make([]func() error, 0, len(db.freeConn))
	for _, dc := range db.freeConn {
		fns = append(fns, dc.closeDBLocked())
	}
	db.freeConn = nil
	db.closed = true
	db.connRequests.CloseAndRemoveAll()
	db.mu.Unlock()
	for _, fn := range fns {
		err1 := fn()
		if err1 != nil {
			err = err1
		}
	}
	db.stop()
	if c, ok := db.connector.(io.Closer); ok {
		err1 := c.Close()
		if err1 != nil {
			err = err1
		}
	}
	return err
}

const defaultMaxIdleConns = 2

func (db *DB) maxIdleConnsLocked() int {
	n := db.maxIdleCount
	switch {
	case n == 0:
		// TODO(bradfitz)：如果驱动支持，询问其默认偏好
		return defaultMaxIdleConns
	case n < 0:
		return 0
	default:
		return n
	}
}

func (db *DB) shortestIdleTimeLocked() time.Duration {
	if db.maxIdleTime <= 0 {
		return db.maxLifetime
	}
	if db.maxLifetime <= 0 {
		return db.maxIdleTime
	}
	return min(db.maxIdleTime, db.maxLifetime)
}

// SetMaxIdleConns 设置空闲连接池中连接的最大数量。
//
// 如果 MaxOpenConns 大于 0 但小于新的 MaxIdleConns，
// 那么新的 MaxIdleConns 会被缩减以匹配 MaxOpenConns 限制。
//
// 如果 n <= 0，则不保留任何空闲连接。
//
// 默认的最大空闲连接数目前为 2。这可能在
// 未来版本中改变。
func (db *DB) SetMaxIdleConns(n int) {
	db.mu.Lock()
	if n > 0 {
		db.maxIdleCount = n
	} else {
		// 没有空闲连接。
		db.maxIdleCount = -1
	}
	// 确保 maxIdle 不超过 maxOpen
	if db.maxOpen > 0 && db.maxIdleConnsLocked() > db.maxOpen {
		db.maxIdleCount = db.maxOpen
	}
	var closing []*driverConn
	idleCount := len(db.freeConn)
	maxIdle := db.maxIdleConnsLocked()
	if idleCount > maxIdle {
		closing = db.freeConn[maxIdle:]
		db.freeConn = db.freeConn[:maxIdle]
	}
	db.maxIdleClosed += int64(len(closing))
	db.mu.Unlock()
	for _, c := range closing {
		c.Close()
	}
}

// SetMaxOpenConns 设置到数据库的打开连接的最大数量。
//
// 如果 MaxIdleConns 大于 0 且新的 MaxOpenConns 小于
// MaxIdleConns，那么 MaxIdleConns 会被缩减以匹配新的
// MaxOpenConns 限制。
//
// 如果 n <= 0，则对打开连接的数量没有限制。
// 默认值为 0（无限制）。
func (db *DB) SetMaxOpenConns(n int) {
	db.mu.Lock()
	db.maxOpen = n
	if n < 0 {
		db.maxOpen = 0
	}
	syncMaxIdle := db.maxOpen > 0 && db.maxIdleConnsLocked() > db.maxOpen
	db.mu.Unlock()
	if syncMaxIdle {
		db.SetMaxIdleConns(n)
	}
}

// SetConnMaxLifetime 设置连接可被复用的最长时间。
//
// 已过期的连接可能会在复用前被延迟关闭。
//
// 如果 d <= 0，则连接不会因存在时间过长而被关闭。
func (db *DB) SetConnMaxLifetime(d time.Duration) {
	if d < 0 {
		d = 0
	}
	db.mu.Lock()
	// 当生命周期被缩短时唤醒清理器。
	if d > 0 && d < db.shortestIdleTimeLocked() && db.cleanerCh != nil {
		select {
		case db.cleanerCh <- struct{}{}:
		default:
		}
	}
	db.maxLifetime = d
	db.startCleanerLocked()
	db.mu.Unlock()
}

// SetConnMaxIdleTime 设置连接可处于空闲状态的最长时间。
//
// 已过期的连接可能会在复用前被延迟关闭。
//
// 如果 d <= 0，则连接不会因空闲时间过长而被关闭。
func (db *DB) SetConnMaxIdleTime(d time.Duration) {
	if d < 0 {
		d = 0
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	// 当空闲时间被缩短时唤醒清理器。
	if d > 0 && d < db.shortestIdleTimeLocked() && db.cleanerCh != nil {
		select {
		case db.cleanerCh <- struct{}{}:
		default:
		}
	}
	db.maxIdleTime = d
	db.startCleanerLocked()
}

// startCleanerLocked 在需要时启动 connectionCleaner。
func (db *DB) startCleanerLocked() {
	if (db.maxLifetime > 0 || db.maxIdleTime > 0) && db.numOpen > 0 && db.cleanerCh == nil {
		db.cleanerCh = make(chan struct{}, 1)
		go db.connectionCleaner(db.shortestIdleTimeLocked())
	}
}

func (db *DB) connectionCleaner(d time.Duration) {
	const minInterval = time.Second

	if d < minInterval {
		d = minInterval
	}
	t := time.NewTimer(d)

	for {
		select {
		case <-t.C:
		case <-db.cleanerCh: // maxLifetime 被更改或 db 被关闭。
		}

		db.mu.Lock()

		d = db.shortestIdleTimeLocked()
		if db.closed || db.numOpen == 0 || d <= 0 {
			db.cleanerCh = nil
			db.mu.Unlock()
			return
		}

		d, closing := db.connectionCleanerRunLocked(d)
		db.mu.Unlock()
		for _, c := range closing {
			c.Close()
		}

		if d < minInterval {
			d = minInterval
		}

		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(d)
	}
}

// connectionCleanerRunLocked 从 freeConn 中移除应当关闭的连接，
// 并将它们与更新后的下次检查时长一起返回；
// 如果需要更快的检查以确保连接被恰当地检查。
func (db *DB) connectionCleanerRunLocked(d time.Duration) (time.Duration, []*driverConn) {
	var idleClosing int64
	var closing []*driverConn
	if db.maxIdleTime > 0 {
		// 由于 freeConn 按 returnedAt 排序，因此按
		// 逆序处理以尽量减少工作量。
		idleSince := nowFunc().Add(-db.maxIdleTime)
		last := len(db.freeConn) - 1
		for i := last; i >= 0; i-- {
			c := db.freeConn[i]
			if c.returnedAt.Before(idleSince) {
				i++
				closing = db.freeConn[:i:i]
				db.freeConn = db.freeConn[i:]
				idleClosing = int64(len(closing))
				db.maxIdleTimeClosed += idleClosing
				break
			}
		}

		if len(db.freeConn) > 0 {
			c := db.freeConn[0]
			if d2 := c.returnedAt.Sub(idleSince); d2 < d {
				// 确保空闲连接尽快被清理。
				d = d2
			}
		}
	}

	if db.maxLifetime > 0 {
		expiredSince := nowFunc().Add(-db.maxLifetime)
		for i := 0; i < len(db.freeConn); i++ {
			c := db.freeConn[i]
			if c.createdAt.Before(expiredSince) {
				closing = append(closing, c)

				last := len(db.freeConn) - 1
				// 使用慢速删除，因为需要保持顺序以确保连接按空闲时间最短优先被复用。
				copy(db.freeConn[i:], db.freeConn[i+1:])
				db.freeConn[last] = nil
				db.freeConn = db.freeConn[:last]
				i--
			} else if d2 := c.createdAt.Sub(expiredSince); d2 < d {
				// 连接已过期时，通过更新下次截止时间 d 防止它们滞留在 freeConn 中。
				d = d2
			}
		}
		db.maxLifetimeClosed += int64(len(closing)) - idleClosing
	}

	return d, closing
}

// DBStats 包含数据库统计信息。
type DBStats struct {
	MaxOpenConnections int // 到数据库的打开连接的最大数量。

	// 池状态
	OpenConnections int // 已建立连接的数量，包括使用中和空闲的。
	InUse           int // 当前正在使用的连接数量。
	Idle            int // 空闲连接的数量。

	// 计数器
	WaitCount         int64         // 等待过的连接总数。
	WaitDuration      time.Duration // 等待新连接而被阻塞的总时间。
	MaxIdleClosed     int64         // 因 SetMaxIdleConns 而被关闭的连接总数。
	MaxIdleTimeClosed int64         // 因 SetConnMaxIdleTime 而被关闭的连接总数。
	MaxLifetimeClosed int64         // 因 SetConnMaxLifetime 而被关闭的连接总数。
}

// Stats 返回数据库统计信息。
func (db *DB) Stats() DBStats {
	wait := db.waitDuration.Load()

	db.mu.Lock()
	defer db.mu.Unlock()

	stats := DBStats{
		MaxOpenConnections: db.maxOpen,

		Idle:            len(db.freeConn),
		OpenConnections: db.numOpen,
		InUse:           db.numOpen - len(db.freeConn),

		WaitCount:         db.waitCount,
		WaitDuration:      time.Duration(wait),
		MaxIdleClosed:     db.maxIdleClosed,
		MaxIdleTimeClosed: db.maxIdleTimeClosed,
		MaxLifetimeClosed: db.maxLifetimeClosed,
	}
	return stats
}

// 假定 db.mu 已加锁。
// 如果存在 connRequests 且尚未达到连接限制，
// 则通知 connectionOpener 打开新连接。
func (db *DB) maybeOpenNewConnections() {
	numRequests := db.connRequests.Len()
	if db.maxOpen > 0 {
		numCanOpen := db.maxOpen - db.numOpen
		if numRequests > numCanOpen {
			numRequests = numCanOpen
		}
	}
	for numRequests > 0 {
		db.numOpen++ // 乐观地
		numRequests--
		if db.closed {
			return
		}
		db.openerCh <- struct{}{}
	}
}

// 在单独的 goroutine 中运行，在需要时打开新连接。
func (db *DB) connectionOpener(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-db.openerCh:
			db.openNewConnection(ctx)
		}
	}
}

// 打开一个新连接
func (db *DB) openNewConnection(ctx context.Context) {
	// maybeOpenNewConnections 在向 db.openerCh 发送之前已经执行了
	// db.numOpen++。如果连接失败或在返回之前被关闭，
	// 此函数必须执行 db.numOpen--。
	ci, err := db.connector.Connect(ctx)
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		if err == nil {
			ci.Close()
		}
		db.numOpen--
		return
	}
	if err != nil {
		db.numOpen--
		db.putConnDBLocked(nil, err)
		db.maybeOpenNewConnections()
		return
	}
	dc := &driverConn{
		db:         db,
		createdAt:  nowFunc(),
		returnedAt: nowFunc(),
		ci:         ci,
	}
	if db.putConnDBLocked(dc, err) {
		db.addDepLocked(dc, dc)
	} else {
		db.numOpen--
		ci.Close()
	}
}

// connRequest 表示对一个新连接的请求。
// 当没有可用的空闲连接时，DB.conn 会创建一个新的
// connRequest 并将其放入 db.connRequests 列表。
type connRequest struct {
	conn *driverConn
	err  error
}

var errDBClosed = errors.New("sql: database is closed")

// conn 返回一个新打开的或缓存的 *driverConn。
func (db *DB) conn(ctx context.Context, strategy connReuseStrategy) (*driverConn, error) {
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil, errDBClosed
	}
	// 检查 context 是否已过期。
	select {
	default:
	case <-ctx.Done():
		db.mu.Unlock()
		return nil, ctx.Err()
	}
	lifetime := db.maxLifetime

	// 如有可能，优先使用空闲连接。
	last := len(db.freeConn) - 1
	if strategy == cachedOrNewConn && last >= 0 {
		// 复用空闲时间最短的连接，以便我们尽快关闭
		// 仍然空闲的连接。
		conn := db.freeConn[last]
		db.freeConn = db.freeConn[:last]
		conn.inUse = true
		if conn.expired(lifetime) {
			db.maxLifetimeClosed++
			db.mu.Unlock()
			conn.Close()
			return nil, driver.ErrBadConn
		}
		db.mu.Unlock()

		// 如有需要，重置会话。
		if err := conn.resetSession(ctx); errors.Is(err, driver.ErrBadConn) {
			conn.Close()
			return nil, err
		}

		return conn, nil
	}

	// 没有空闲连接，或者被要求不要使用空闲连接。如果我们
	// 不被允许再打开任何连接，就发起一个请求并等待。
	if db.maxOpen > 0 && db.numOpen >= db.maxOpen {
		// 创建 connRequest channel。它是带缓冲的，这样
		// connectionOpener 在等待 req 被读取时不会阻塞。
		req := make(chan connRequest, 1)
		delHandle := db.connRequests.Add(req)
		db.waitCount++
		db.mu.Unlock()

		waitStart := nowFunc()

		// 使用 context 为连接请求设置超时。
		select {
		case <-ctx.Done():
			// 移除连接请求，并确保移除之后
			// 没有值被发送到它上面。
			db.mu.Lock()
			deleted := db.connRequests.Delete(delHandle)
			db.mu.Unlock()

			db.waitDuration.Add(int64(time.Since(waitStart)))

			// 如果我们删除失败，那意味着要么 DB 被关闭了，要么
			// 其他什么东西抢占了它并即将向它发送数据。
			if !deleted {
				// TODO(bradfitz)：与其使用这种尽力而为的 select，我们
				// 或许应该启动一个 goroutine 从 req 读取。这种尽力而为的
				// select 在引入检查 'deleted' 的改动之前就已存在。
				// 但如果我们确定它没有被删除且仍有发送方未完成，
				// 我们或许应该（在新的 goroutine 中）阻塞在 req 上
				// 以取回该连接。
				select {
				default:
				case ret, ok := <-req:
					if ok && ret.conn != nil {
						db.putConn(ret.conn, ret.err, false)
					}
				}
			}
			return nil, ctx.Err()
		case ret, ok := <-req:
			db.waitDuration.Add(int64(time.Since(waitStart)))

			if !ok {
				return nil, errDBClosed
			}
			// 仅当策略为 cachedOrNewConns 时才检查连接是否过期。
			// 如果我们需要一个新连接，就直接复用该连接而不查看
			// 过期时间。如果它已过期，它会在被放回连接池时
			// 被检查。
			// 这优先考虑将有效连接提供给客户端，而不是严格遵循连接
			// 生命周期，因为连接无论如何都可能恰好在此时之后过期。
			if strategy == cachedOrNewConn && ret.err == nil && ret.conn.expired(lifetime) {
				db.mu.Lock()
				db.maxLifetimeClosed++
				db.mu.Unlock()
				ret.conn.Close()
				return nil, driver.ErrBadConn
			}
			if ret.conn == nil {
				return nil, ret.err
			}

			// 如有需要，重置会话。
			if err := ret.conn.resetSession(ctx); errors.Is(err, driver.ErrBadConn) {
				ret.conn.Close()
				return nil, err
			}
			return ret.conn, ret.err
		}
	}

	db.numOpen++ // 乐观地
	db.mu.Unlock()
	ci, err := db.connector.Connect(ctx)
	if err != nil {
		db.mu.Lock()
		db.numOpen-- // 修正之前的乐观计数
		db.maybeOpenNewConnections()
		db.mu.Unlock()
		return nil, err
	}
	db.mu.Lock()
	dc := &driverConn{
		db:         db,
		createdAt:  nowFunc(),
		returnedAt: nowFunc(),
		ci:         ci,
		inUse:      true,
	}
	db.addDepLocked(dc, dc)
	db.mu.Unlock()
	return dc, nil
}

// putConnHook 是用于测试的钩子。
var putConnHook func(*DB, *driverConn)

// noteUnusedDriverStatement 记录 ds 不再被使用，应尽可能
// 关闭它（当 c 下次不被使用时），除非 c
// 已经被关闭。
func (db *DB) noteUnusedDriverStatement(c *driverConn, ds *driverStmt) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if c.inUse {
		c.onPut = append(c.onPut, func() {
			ds.Close()
		})
	} else {
		c.Lock()
		fc := c.finalClosed
		c.Unlock()
		if !fc {
			ds.Close()
		}
	}
}

// debugGetPut 决定是否返回 getConn 和 putConn 调用的堆栈跟踪，
// 以获得更详细的崩溃信息。
const debugGetPut = false

// putConn 将连接添加到 db 的空闲池。
// err 可选地表示该连接上发生的最后一个错误。
func (db *DB) putConn(dc *driverConn, err error, resetSession bool) {
	if !errors.Is(err, driver.ErrBadConn) {
		if !dc.validateConnection(resetSession) {
			err = driver.ErrBadConn
		}
	}
	db.mu.Lock()
	if !dc.inUse {
		db.mu.Unlock()
		if debugGetPut {
			fmt.Printf("putConn(%v) DUPLICATE was: %s\n\nPREVIOUS was: %s", dc, stack(), db.lastPut[dc])
		}
		panic("sql: connection returned that was never out")
	}

	if !errors.Is(err, driver.ErrBadConn) && dc.expired(db.maxLifetime) {
		db.maxLifetimeClosed++
		err = driver.ErrBadConn
	}
	if debugGetPut {
		db.lastPut[dc] = stack()
	}
	dc.inUse = false
	dc.returnedAt = nowFunc()

	for _, fn := range dc.onPut {
		fn()
	}
	dc.onPut = nil

	if errors.Is(err, driver.ErrBadConn) {
		// 不要复用失效的连接。
		// 由于该 conn 被视为失效并将被丢弃，因此将其
		// 视为已关闭。此处不要递减打开计数，finalClose 会
		// 处理它。
		db.maybeOpenNewConnections()
		db.mu.Unlock()
		dc.Close()
		return
	}
	if putConnHook != nil {
		putConnHook(db, dc)
	}
	added := db.putConnDBLocked(dc, nil)
	db.mu.Unlock()

	if !added {
		dc.Close()
		return
	}
}

// 满足一个 connRequest，或把 driverConn 放入空闲池并返回 true，
// 否则返回 false。
// putConnDBLocked 会在存在 connRequest 时满足它，或者如果 err == nil
// 且不会超过空闲连接限制，则把 *driverConn 返回到 freeConn 列表。
// 如果 err != nil，dc 的值会被忽略。
// 如果 err == nil，则 dc 不能等于 nil。
// 如果满足了一个 connRequest，或 *driverConn 被放入
// freeConn 列表，则返回 true，否则返回 false。
func (db *DB) putConnDBLocked(dc *driverConn, err error) bool {
	if db.closed {
		return false
	}
	if db.maxOpen > 0 && db.numOpen > db.maxOpen {
		return false
	}
	if req, ok := db.connRequests.TakeRandom(); ok {
		if err == nil {
			dc.inUse = true
		}
		req <- connRequest{
			conn: dc,
			err:  err,
		}
		return true
	} else if err == nil && !db.closed {
		if db.maxIdleConnsLocked() > len(db.freeConn) {
			db.freeConn = append(db.freeConn, dc)
			db.startCleanerLocked()
			return true
		}
		db.maxIdleClosed++
	}
	return false
}

// maxBadConnRetries 是驱动返回 driver.ErrBadConn 以表示连接已损坏时
// 在强制打开新连接之前允许的最大重试次数。
const maxBadConnRetries = 2

func (db *DB) retry(fn func(strategy connReuseStrategy) error) error {
	for i := int64(0); i < maxBadConnRetries; i++ {
		err := fn(cachedOrNewConn)
		// 如果 err 是 driver.ErrBadConn 则重试
		if err == nil || !errors.Is(err, driver.ErrBadConn) {
			return err
		}
	}

	return fn(alwaysNewConn)
}

// PrepareContext 创建一个已准备的语句，供后续查询或执行使用。
// 多个查询或执行可以在返回的语句上并发运行。
// 当不再需要该语句时，调用方必须调用该语句的
// [*Stmt.Close] 方法。
//
// 提供的 context 用于该语句的准备，而不是
// 该语句的执行。
func (db *DB) PrepareContext(ctx context.Context, query string) (*Stmt, error) {
	var stmt *Stmt
	var err error

	err = db.retry(func(strategy connReuseStrategy) error {
		stmt, err = db.prepare(ctx, query, strategy)
		return err
	})

	return stmt, err
}

// Prepare 创建一个已准备的语句，供后续查询或执行使用。
// 多个查询或执行可以在返回的语句上并发运行。
// 当不再需要该语句时，调用方必须调用该语句的
// [*Stmt.Close] 方法。
//
// Prepare 内部使用 [context.Background]；要指定 context，请使用
// [DB.PrepareContext]。
func (db *DB) Prepare(query string) (*Stmt, error) {
	return db.PrepareContext(context.Background(), query)
}

func (db *DB) prepare(ctx context.Context, query string, strategy connReuseStrategy) (*Stmt, error) {
	// TODO：检查 db.driver 是否支持可选的
	// driver.Preparer 接口，如果支持则改为调用它，
	// 否则我们创建一个绑定到连接的已准备语句，
	// 而要执行这个已准备语句，我们要么需要使用这个连接
	// （如果它空闲），要么获取一个新连接 + 重新准备 + 在其上执行。
	dc, err := db.conn(ctx, strategy)
	if err != nil {
		return nil, err
	}
	return db.prepareDC(ctx, dc, dc.releaseConn, nil, query)
}

// prepareDC 在 driverConn 上准备查询，并在返回之前调用 release。
// 当 cg == nil 时，意味着使用连接池；
// 当 cg != nil 时，只使用单个驱动连接。
func (db *DB) prepareDC(ctx context.Context, dc *driverConn, release func(error), cg stmtConnGrabber, query string) (*Stmt, error) {
	var ds *driverStmt
	var err error
	defer func() {
		release(err)
	}()
	withLock(dc, func() {
		ds, err = dc.prepareLocked(ctx, cg, query)
	})
	if err != nil {
		return nil, err
	}
	stmt := &Stmt{
		db:    db,
		query: query,
		cg:    cg,
		cgds:  ds,
	}

	// 当 cg == nil 时，该语句需要跟踪它们在各种连接上
	// 被准备的情况，并在 DB 上记录 stmt 依赖。
	if cg == nil {
		stmt.css = []connStmt{{dc, ds}}
		stmt.lastNumClosed = db.numClosed.Load()
		db.addDep(stmt, stmt)
	}
	return stmt, nil
}

// ExecContext 执行一个不返回任何行的查询。
// args 用于查询中的任何占位符参数。
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (Result, error) {
	var res Result
	var err error

	err = db.retry(func(strategy connReuseStrategy) error {
		res, err = db.exec(ctx, query, args, strategy)
		return err
	})

	return res, err
}

// Exec 执行一个不返回任何行的查询。
// args 用于查询中的任何占位符参数。
//
// Exec 内部使用 [context.Background]；要指定 context，请使用
// [DB.ExecContext]。
func (db *DB) Exec(query string, args ...any) (Result, error) {
	return db.ExecContext(context.Background(), query, args...)
}

func (db *DB) exec(ctx context.Context, query string, args []any, strategy connReuseStrategy) (Result, error) {
	dc, err := db.conn(ctx, strategy)
	if err != nil {
		return nil, err
	}
	return db.execDC(ctx, dc, dc.releaseConn, query, args)
}

func (db *DB) execDC(ctx context.Context, dc *driverConn, release func(error), query string, args []any) (res Result, err error) {
	defer func() {
		release(err)
	}()
	execerCtx, ok := dc.ci.(driver.ExecerContext)
	var execer driver.Execer
	if !ok {
		execer, ok = dc.ci.(driver.Execer)
	}
	if ok {
		var nvdargs []driver.NamedValue
		var resi driver.Result
		withLock(dc, func() {
			nvdargs, err = driverArgsConnLocked(dc.ci, nil, args)
			if err != nil {
				return
			}
			resi, err = ctxDriverExec(ctx, execerCtx, execer, query, nvdargs)
		})
		if err != driver.ErrSkip {
			if err != nil {
				return nil, err
			}
			return driverResult{dc, resi}, nil
		}
	}

	var si driver.Stmt
	withLock(dc, func() {
		si, err = ctxDriverPrepare(ctx, dc.ci, query)
	})
	if err != nil {
		return nil, err
	}
	ds := &driverStmt{Locker: dc, si: si}
	defer ds.Close()
	return resultFromStatement(ctx, dc.ci, ds, args...)
}

// QueryContext 执行一个返回行的查询，通常是 SELECT。
// args 用于查询中的任何占位符参数。
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*Rows, error) {
	var rows *Rows
	var err error

	err = db.retry(func(strategy connReuseStrategy) error {
		rows, err = db.query(ctx, query, args, strategy)
		return err
	})

	return rows, err
}

// Query 执行一个返回行的查询，通常是 SELECT。
// args 用于查询中的任何占位符参数。
//
// Query 内部使用 [context.Background]；要指定 context，请使用
// [DB.QueryContext]。
func (db *DB) Query(query string, args ...any) (*Rows, error) {
	return db.QueryContext(context.Background(), query, args...)
}

func (db *DB) query(ctx context.Context, query string, args []any, strategy connReuseStrategy) (*Rows, error) {
	dc, err := db.conn(ctx, strategy)
	if err != nil {
		return nil, err
	}

	return db.queryDC(ctx, nil, dc, dc.releaseConn, query, args)
}

// queryDC 在给定连接上执行查询。
// 该连接由 releaseConn 函数释放。
// ctx 来自查询方法，txctx 来自
// 可选的事务 context。
func (db *DB) queryDC(ctx, txctx context.Context, dc *driverConn, releaseConn func(error), query string, args []any) (*Rows, error) {
	queryerCtx, ok := dc.ci.(driver.QueryerContext)
	var queryer driver.Queryer
	if !ok {
		queryer, ok = dc.ci.(driver.Queryer)
	}
	if ok {
		var nvdargs []driver.NamedValue
		var rowsi driver.Rows
		var err error
		withLock(dc, func() {
			nvdargs, err = driverArgsConnLocked(dc.ci, nil, args)
			if err != nil {
				return
			}
			rowsi, err = ctxDriverQuery(ctx, queryerCtx, queryer, query, nvdargs)
		})
		if err != driver.ErrSkip {
			if err != nil {
				releaseConn(err)
				return nil, err
			}
			// 注意：dc 的所有权传递给 *Rows，由 releaseConn 负责释放。
			rows := &Rows{
				dc:          dc,
				releaseConn: releaseConn,
				rowsi:       rowsi,
			}
			rows.initContextClose(ctx, txctx)
			return rows, nil
		}
	}

	var si driver.Stmt
	var err error
	withLock(dc, func() {
		si, err = ctxDriverPrepare(ctx, dc.ci, query)
	})
	if err != nil {
		releaseConn(err)
		return nil, err
	}

	ds := &driverStmt{Locker: dc, si: si}
	rowsi, err := rowsiFromStatement(ctx, dc.ci, ds, args...)
	if err != nil {
		ds.Close()
		releaseConn(err)
		return nil, err
	}

	// 注意：ci 的所有权传递给 *Rows，由 releaseConn 负责释放。
	rows := &Rows{
		dc:          dc,
		releaseConn: releaseConn,
		rowsi:       rowsi,
		closeStmt:   ds,
	}
	rows.initContextClose(ctx, txctx)
	return rows, nil
}

// QueryRowContext 执行一个预期最多返回一行的查询。
// QueryRowContext 始终返回非 nil 值。错误会被推迟到
// 调用 [Row] 的 Scan 方法时才产生。
// 如果查询没有选中任何行，[*Row.Scan] 将返回 [ErrNoRows]。
// 否则，[*Row.Scan] 扫描第一个选中的行并丢弃
// 其余的行。
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *Row {
	rows, err := db.QueryContext(ctx, query, args...)
	return &Row{rows: rows, err: err}
}

// QueryRow 执行一个预期最多返回一行的查询。
// QueryRow 始终返回非 nil 值。错误会被推迟到
// 调用 [Row] 的 Scan 方法时才产生。
// 如果查询没有选中任何行，[*Row.Scan] 将返回 [ErrNoRows]。
// 否则，[*Row.Scan] 扫描第一个选中的行并丢弃
// 其余的行。
//
// QueryRow 内部使用 [context.Background]；要指定 context，请使用
// [DB.QueryRowContext]。
func (db *DB) QueryRow(query string, args ...any) *Row {
	return db.QueryRowContext(context.Background(), query, args...)
}

// BeginTx 启动一个事务。
//
// 提供的 context 会一直使用到事务被提交或回滚。
// 如果 context 被取消，sql 包将回滚该事务。
// 如果提供给 BeginTx 的 context 被取消，
// [Tx.Commit] 将返回错误。
//
// 提供的 [TxOptions] 是可选的，如果应使用默认值则可以为 nil。
// 如果使用了驱动不支持的非默认隔离级别，
// 将返回错误。
func (db *DB) BeginTx(ctx context.Context, opts *TxOptions) (*Tx, error) {
	var tx *Tx
	var err error

	err = db.retry(func(strategy connReuseStrategy) error {
		tx, err = db.begin(ctx, opts, strategy)
		return err
	})

	return tx, err
}

// Begin 启动一个事务。默认隔离级别取决于
// 驱动。
//
// Begin 内部使用 [context.Background]；要指定 context，请使用
// [DB.BeginTx]。
func (db *DB) Begin() (*Tx, error) {
	return db.BeginTx(context.Background(), nil)
}

func (db *DB) begin(ctx context.Context, opts *TxOptions, strategy connReuseStrategy) (tx *Tx, err error) {
	dc, err := db.conn(ctx, strategy)
	if err != nil {
		return nil, err
	}
	return db.beginDC(ctx, dc, dc.releaseConn, opts)
}

// beginDC 启动一个事务。提供的 dc 必须有效且可以使用。
func (db *DB) beginDC(ctx context.Context, dc *driverConn, release func(error), opts *TxOptions) (tx *Tx, err error) {
	var txi driver.Tx
	keepConnOnRollback := false
	withLock(dc, func() {
		_, hasSessionResetter := dc.ci.(driver.SessionResetter)
		_, hasConnectionValidator := dc.ci.(driver.Validator)
		keepConnOnRollback = hasSessionResetter && hasConnectionValidator
		txi, err = ctxDriverBegin(ctx, opts, dc.ci)
	})
	if err != nil {
		release(err)
		return nil, err
	}

	// 安排事务在 context 被取消时回滚。
	// Tx 中的 cancel 函数会在 done 被设置为 true 之后调用。
	ctx, cancel := context.WithCancel(ctx)
	tx = &Tx{
		db:                 db,
		dc:                 dc,
		releaseConn:        release,
		txi:                txi,
		cancel:             cancel,
		keepConnOnRollback: keepConnOnRollback,
		ctx:                ctx,
	}
	go tx.awaitDone()
	return tx, nil
}

// Driver 返回数据库的底层驱动。
func (db *DB) Driver() driver.Driver {
	return db.connector.Driver()
}

// ErrConnDone 由任何在已归还到连接池的连接上执行的操作返回。
var ErrConnDone = errors.New("sql: connection is already closed")

// Conn 通过打开新连接或从连接池返回现有连接来返回单个连接。
// Conn 会阻塞，直到返回一个连接或 ctx 被取消。
// 在同一个 Conn 上运行的查询将在同一个数据库会话中运行。
//
// 每个 Conn 使用后都必须通过调用 [Conn.Close]
// 归还到数据库池。
func (db *DB) Conn(ctx context.Context) (*Conn, error) {
	var dc *driverConn
	var err error

	err = db.retry(func(strategy connReuseStrategy) error {
		dc, err = db.conn(ctx, strategy)
		return err
	})

	if err != nil {
		return nil, err
	}

	conn := &Conn{
		db: db,
		dc: dc,
	}
	return conn, nil
}

type releaseConn func(error)

// Conn 表示单个数据库连接，而不是数据库连接池。
// 除非确实需要持续的单个数据库连接，否则
// 优先使用 [DB] 运行查询。
//
// Conn 必须调用 [Conn.Close] 将连接归还到数据库池，
// 并且可以在有查询运行的同时这样做。
//
// 调用 [Conn.Close] 之后，对该连接的所有操作
// 都会以 [ErrConnDone] 失败。
type Conn struct {
	db *DB

	// closemu 防止在有活动查询时关闭连接。
	// 它在查询期间以读模式持有，
	// 在关闭时以独占模式持有。
	closemu sync.RWMutex

	// dc 在关闭之前被持有，关闭时
	// 它会被归还到连接池。
	dc *driverConn

	// done 在关闭时从 false 精确地转变为 true 一次。
	// 一旦 done，所有操作都会以 ErrConnDone 失败。
	done atomic.Bool

	releaseConnOnce sync.Once
	// releaseConnCache 是 c.closemuRUnlockCondReleaseConn 的缓存，
	// 用于在调用 grabConn 时节省分配。
	releaseConnCache releaseConn
}

// grabConn 接收一个 context 以实现 stmtConnGrabber，
// 但该 context 并未被使用。
func (c *Conn) grabConn(context.Context) (*driverConn, releaseConn, error) {
	if c.done.Load() {
		return nil, nil, ErrConnDone
	}
	c.releaseConnOnce.Do(func() {
		c.releaseConnCache = c.closemuRUnlockCondReleaseConn
	})
	c.closemu.RLock()
	return c.dc, c.releaseConnCache, nil
}

// PingContext 验证到数据库的连接仍然有效。
func (c *Conn) PingContext(ctx context.Context) error {
	dc, release, err := c.grabConn(ctx)
	if err != nil {
		return err
	}
	return c.db.pingDC(ctx, dc, release)
}

// ExecContext 执行一个不返回任何行的查询。
// args 用于查询中的任何占位符参数。
func (c *Conn) ExecContext(ctx context.Context, query string, args ...any) (Result, error) {
	dc, release, err := c.grabConn(ctx)
	if err != nil {
		return nil, err
	}
	return c.db.execDC(ctx, dc, release, query, args)
}

// QueryContext 执行一个返回行的查询，通常是 SELECT。
// args 用于查询中的任何占位符参数。
func (c *Conn) QueryContext(ctx context.Context, query string, args ...any) (*Rows, error) {
	dc, release, err := c.grabConn(ctx)
	if err != nil {
		return nil, err
	}
	return c.db.queryDC(ctx, nil, dc, release, query, args)
}

// QueryRowContext 执行一个预期最多返回一行的查询。
// QueryRowContext 始终返回非 nil 值。错误会被推迟到
// 调用 [*Row.Scan] 方法时才产生。
// 如果查询没有选中任何行，[*Row.Scan] 将返回 [ErrNoRows]。
// 否则，[*Row.Scan] 扫描第一个选中的行并丢弃
// 其余的行。
func (c *Conn) QueryRowContext(ctx context.Context, query string, args ...any) *Row {
	rows, err := c.QueryContext(ctx, query, args...)
	return &Row{rows: rows, err: err}
}

// PrepareContext 创建一个已准备的语句，供后续查询或执行使用。
// 多个查询或执行可以在返回的语句上并发运行。
// 当不再需要该语句时，调用方必须调用该语句的
// [*Stmt.Close] 方法。
//
// 提供的 context 用于该语句的准备，而不是
// 该语句的执行。
func (c *Conn) PrepareContext(ctx context.Context, query string) (*Stmt, error) {
	dc, release, err := c.grabConn(ctx)
	if err != nil {
		return nil, err
	}
	return c.db.prepareDC(ctx, dc, release, c, query)
}

// Raw 在 f 的执行期间执行 f 并暴露底层驱动连接。
// driverConn 不得在 f 之外使用。
//
// 一旦 f 返回且 err 不是 [driver.ErrBadConn]，[Conn] 将继续可用，
// 直到调用 [Conn.Close]。
func (c *Conn) Raw(f func(driverConn any) error) (err error) {
	var dc *driverConn
	var release releaseConn

	// grabConn 接收一个 context 以实现 stmtConnGrabber，但该 context 并未被使用。
	dc, release, err = c.grabConn(nil)
	if err != nil {
		return
	}
	fPanic := true
	dc.Mutex.Lock()
	defer func() {
		dc.Mutex.Unlock()

		// 如果 f panic，fPanic 将保持为 true。
		// 确保向 release 传递一个错误，以便该连接
		// 可以被丢弃。
		if fPanic {
			err = driver.ErrBadConn
		}
		release(err)
	}()
	err = f(dc.ci)
	fPanic = false

	return
}

// BeginTx 启动一个事务。
//
// 提供的 context 会一直使用到事务被提交或回滚。
// 如果 context 被取消，sql 包将回滚该事务。
// 如果提供给 BeginTx 的 context 被取消，
// [Tx.Commit] 将返回错误。
//
// 提供的 [TxOptions] 是可选的，如果应使用默认值则可以为 nil。
// 如果使用了驱动不支持的非默认隔离级别，
// 将返回错误。
func (c *Conn) BeginTx(ctx context.Context, opts *TxOptions) (*Tx, error) {
	dc, release, err := c.grabConn(ctx)
	if err != nil {
		return nil, err
	}
	return c.db.beginDC(ctx, dc, release, opts)
}

// closemuRUnlockCondReleaseConn 以读模式解锁 closemu，
// 因为对 dc 的 sql 操作已完成。
func (c *Conn) closemuRUnlockCondReleaseConn(err error) {
	c.closemu.RUnlock()
	if errors.Is(err, driver.ErrBadConn) {
		c.close(err)
	}
}

func (c *Conn) txCtx() context.Context {
	return nil
}

func (c *Conn) close(err error) error {
	if !c.done.CompareAndSwap(false, true) {
		return ErrConnDone
	}

	// 在释放驱动连接前后加锁，
	// 以确保在释放之前所有查询都已停止。
	c.closemu.Lock()
	defer c.closemu.Unlock()

	c.dc.releaseConn(err)
	c.dc = nil
	c.db = nil
	return err
}

// Close 将连接归还到连接池。
// Close 之后的所有操作都将以 [ErrConnDone] 返回。
// Close 可以与其他操作并发安全地调用，并且会
// 阻塞直到所有其他操作完成。先取消任何使用过的
// context，然后紧接着调用 close 可能会有用。
func (c *Conn) Close() error {
	return c.close(nil)
}

// Tx 是一个进行中的数据库事务。
//
// 事务必须以调用 [Tx.Commit] 或 [Tx.Rollback] 结束。
//
// 调用 [Tx.Commit] 或 [Tx.Rollback] 之后，对该事务的所有操作
// 都会以 [ErrTxDone] 失败。
//
// 通过调用事务的 [Tx.Prepare] 或 [Tx.Stmt] 方法为该事务
// 准备的语句，会在调用 [Tx.Commit] 或 [Tx.Rollback] 时
// 被关闭。
type Tx struct {
	db *DB

	// closemu 防止在有活动查询时关闭事务。
	// 它在查询期间以读模式持有，
	// 在关闭时以独占模式持有。
	closemu sync.RWMutex

	// dc 在 Commit 或 Rollback 之前被独占持有，届时
	// 它会被 putConn 归还。
	dc  *driverConn
	txi driver.Tx

	// releaseConn 在 Tx 关闭时被调用，用于将
	// 持有的任何 driverConn 释放回池中。
	releaseConn func(error)

	// done 在 Commit 或 Rollback 时从 false 精确地转变为 true 一次。
	// 一旦 done，所有操作都会以
	// ErrTxDone 失败。
	done atomic.Bool

	// 如果驱动知道如何重置连接的会话并在需要时丢弃
	// 该连接，则 keepConnOnRollback 为 true。
	keepConnOnRollback bool

	// 为此事务准备的所有 Stmt。它们会在事务
	// 被提交或回滚之后被关闭。
	stmts struct {
		sync.Mutex
		v []*Stmt
	}

	// cancel 在 done 从 0 转变为 1 之后被调用。
	cancel func()

	// ctx 在事务的整个生命周期内存续。
	ctx context.Context
}

// awaitDone 阻塞直到 Tx 中的 context 被取消，并在事务尚未完成时
// 回滚该事务。
func (tx *Tx) awaitDone() {
	// 等待事务被提交或回滚，
	// 或者关联的 context 被关闭。
	<-tx.ctx.Done()

	// 丢弃并关闭所使用的连接，以确保事务被关闭
	// 且资源被释放。如果事务已经被提交或回滚，
	// 这次回滚不做任何事情。
	// 如果连接知道如何重置会话，则不要丢弃
	// 该连接。
	discardConnection := !tx.keepConnOnRollback
	tx.rollback(discardConnection)
}

func (tx *Tx) isDone() bool {
	return tx.done.Load()
}

// ErrTxDone 由任何在已提交或已回滚的事务上执行的操作返回。
var ErrTxDone = errors.New("sql: transaction has already been committed or rolled back")

// close 将连接归还到池中，并且
// 只能在 tx 已被取消且不会被并发执行时，
// 由 Tx.rollback 或 Tx.Commit 调用。
func (tx *Tx) close(err error) {
	tx.releaseConn(err)
	tx.dc = nil
	tx.txi = nil
}

// hookTxGrabConn 指定一个可选的钩子，在成功调用
// (*Tx).grabConn 时被调用。用于测试。
var hookTxGrabConn func()

func (tx *Tx) grabConn(ctx context.Context) (*driverConn, releaseConn, error) {
	select {
	default:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}

	// closemu.RLock 必须在 isDone 检查之前进行，以防止 Tx
	// 在有查询执行时被关闭。
	tx.closemu.RLock()
	if tx.isDone() {
		tx.closemu.RUnlock()
		return nil, nil, ErrTxDone
	}
	if hookTxGrabConn != nil { // 测试钩子
		hookTxGrabConn()
	}
	return tx.dc, tx.closemuRUnlockRelease, nil
}

func (tx *Tx) txCtx() context.Context {
	return tx.ctx
}

// closemuRUnlockRelease 在 [DB.ExecContext] 和 [DB.QueryContext] 中
// 作为 func(error) 方法值使用。在 releaseConn 中解锁可以防止驱动 conn
// 在 Rows 被关闭之前被归还到连接池。
func (tx *Tx) closemuRUnlockRelease(error) {
	tx.closemu.RUnlock()
}

// 关闭为此事务准备的所有 Stmt。
func (tx *Tx) closePrepared() {
	tx.stmts.Lock()
	defer tx.stmts.Unlock()
	for _, stmt := range tx.stmts.v {
		stmt.Close()
	}
}

// Commit 提交事务。
func (tx *Tx) Commit() error {
	// 先检查 context 以避免事务泄漏。
	// 如果把它放在 tx.done 的 CompareAndSwap 语句之后，我们就无法确保
	// tx.done 与真正的 COMMIT 操作之间的一致性。
	select {
	default:
	case <-tx.ctx.Done():
		if tx.done.Load() {
			return ErrTxDone
		}
		return tx.ctx.Err()
	}
	if !tx.done.CompareAndSwap(false, true) {
		return ErrTxDone
	}

	// 取消 Tx 以释放任何活动的 R-closemu 锁。
	// 这样做是安全的，因为 tx.done 已经从 0 转变为 1。
	// 在回滚之前持有 W-closemu 锁，
	// 以确保没有其他连接有活动的查询。
	tx.cancel()
	tx.closemu.Lock()
	tx.closemu.Unlock()

	var err error
	withLock(tx.dc, func() {
		err = tx.txi.Commit()
	})
	if !errors.Is(err, driver.ErrBadConn) {
		tx.closePrepared()
	}
	tx.close(err)
	return err
}

var rollbackHook func()

// rollback 中止事务，并可选择强制池丢弃
// 该连接。
func (tx *Tx) rollback(discardConn bool) error {
	if !tx.done.CompareAndSwap(false, true) {
		return ErrTxDone
	}

	if rollbackHook != nil {
		rollbackHook()
	}

	// 取消 Tx 以释放任何活动的 R-closemu 锁。
	// 这样做是安全的，因为 tx.done 已经从 0 转变为 1。
	// 在回滚之前持有 W-closemu 锁，
	// 以确保没有其他连接有活动的查询。
	tx.cancel()
	tx.closemu.Lock()
	tx.closemu.Unlock()

	var err error
	withLock(tx.dc, func() {
		err = tx.txi.Rollback()
	})
	if !errors.Is(err, driver.ErrBadConn) {
		tx.closePrepared()
	}
	if discardConn {
		err = driver.ErrBadConn
	}
	tx.close(err)
	return err
}

// Rollback 中止事务。
func (tx *Tx) Rollback() error {
	return tx.rollback(false)
}

// PrepareContext 创建一个在事务内使用的已准备语句。
//
// 返回的语句在事务内运行，并会在事务被提交或
// 回滚时被关闭。
//
// 要在该事务上使用已有的已准备语句，见 [Tx.Stmt]。
//
// 提供的 context 将用于该 context 的准备，而不是
// 返回语句的执行。返回的语句
// 将在事务 context 中运行。
func (tx *Tx) PrepareContext(ctx context.Context, query string) (*Stmt, error) {
	dc, release, err := tx.grabConn(ctx)
	if err != nil {
		return nil, err
	}

	stmt, err := tx.db.prepareDC(ctx, dc, release, tx, query)
	if err != nil {
		return nil, err
	}
	tx.stmts.Lock()
	tx.stmts.v = append(tx.stmts.v, stmt)
	tx.stmts.Unlock()
	return stmt, nil
}

// Prepare 创建一个在事务内使用的已准备语句。
//
// 返回的语句在事务内运行，并会在事务被提交或
// 回滚时被关闭。
//
// 要在该事务上使用已有的已准备语句，见 [Tx.Stmt]。
//
// Prepare 内部使用 [context.Background]；要指定 context，请使用
// [Tx.PrepareContext]。
func (tx *Tx) Prepare(query string) (*Stmt, error) {
	return tx.PrepareContext(context.Background(), query)
}

// StmtContext 从已有的语句返回一个特定于事务的已准备语句。
//
// 示例：
//
//	updateMoney, err := db.Prepare("UPDATE balance SET money=money+? WHERE id=?")
//	...
//	tx, err := db.Begin()
//	...
//	res, err := tx.StmtContext(ctx, updateMoney).Exec(123.45, 98293203)
//
// 提供的 context 用于该语句的准备，而不是
// 该语句的执行。
//
// 返回的语句在事务内运行，并会在事务被提交或
// 回滚时被关闭。
func (tx *Tx) StmtContext(ctx context.Context, stmt *Stmt) *Stmt {
	dc, release, err := tx.grabConn(ctx)
	if err != nil {
		return &Stmt{stickyErr: err}
	}
	defer release(nil)

	if tx.db != stmt.db {
		return &Stmt{stickyErr: errors.New("sql: Tx.Stmt: statement from different database used")}
	}
	var si driver.Stmt
	var parentStmt *Stmt
	stmt.mu.Lock()
	if stmt.closed || stmt.cg != nil {
		// 如果该语句已被关闭或已属于某个事务，
		// 我们就无法在该连接中复用它。
		// 由于 tx.StmtContext 在调用时不应传入已属于 tx 的
		// Stmt，我们忽略这个边界情况，并在这种情况下
		// 重新准备该语句。无需为此增加
		// 代码复杂度。
		stmt.mu.Unlock()
		withLock(dc, func() {
			si, err = ctxDriverPrepare(ctx, dc.ci, stmt.query)
		})
		if err != nil {
			return &Stmt{stickyErr: err}
		}
	} else {
		stmt.removeClosedStmtLocked()
		// 看看该语句是否已在此连接上准备好，
		// 如果可能则复用它。
		for _, v := range stmt.css {
			if v.dc == dc {
				si = v.ds.si
				break
			}
		}

		stmt.mu.Unlock()

		if si == nil {
			var ds *driverStmt
			withLock(dc, func() {
				ds, err = stmt.prepareOnConnLocked(ctx, dc)
			})
			if err != nil {
				return &Stmt{stickyErr: err}
			}
			si = ds.si
		}
		parentStmt = stmt
	}

	txs := &Stmt{
		db: tx.db,
		cg: tx,
		cgds: &driverStmt{
			Locker: dc,
			si:     si,
		},
		parentStmt: parentStmt,
		query:      stmt.query,
	}
	if parentStmt != nil {
		tx.db.addDep(parentStmt, txs)
	}
	tx.stmts.Lock()
	tx.stmts.v = append(tx.stmts.v, txs)
	tx.stmts.Unlock()
	return txs
}

// Stmt 从已有的语句返回一个特定于事务的已准备语句。
//
// 示例：
//
//	updateMoney, err := db.Prepare("UPDATE balance SET money=money+? WHERE id=?")
//	...
//	tx, err := db.Begin()
//	...
//	res, err := tx.Stmt(updateMoney).Exec(123.45, 98293203)
//
// 返回的语句在事务内运行，并会在事务被提交或
// 回滚时被关闭。
//
// Stmt 内部使用 [context.Background]；要指定 context，请使用
// [Tx.StmtContext]。
func (tx *Tx) Stmt(stmt *Stmt) *Stmt {
	return tx.StmtContext(context.Background(), stmt)
}

// ExecContext 执行一个不返回行的查询。
// 例如：INSERT 和 UPDATE。
func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (Result, error) {
	dc, release, err := tx.grabConn(ctx)
	if err != nil {
		return nil, err
	}
	return tx.db.execDC(ctx, dc, release, query, args)
}

// Exec 执行一个不返回行的查询。
// 例如：INSERT 和 UPDATE。
//
// Exec 内部使用 [context.Background]；要指定 context，请使用
// [Tx.ExecContext]。
func (tx *Tx) Exec(query string, args ...any) (Result, error) {
	return tx.ExecContext(context.Background(), query, args...)
}

// QueryContext 执行一个返回行的查询，通常是 SELECT。
func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*Rows, error) {
	dc, release, err := tx.grabConn(ctx)
	if err != nil {
		return nil, err
	}

	return tx.db.queryDC(ctx, tx.ctx, dc, release, query, args)
}

// Query 执行一个返回行的查询，通常是 SELECT。
//
// Query 内部使用 [context.Background]；要指定 context，请使用
// [Tx.QueryContext]。
func (tx *Tx) Query(query string, args ...any) (*Rows, error) {
	return tx.QueryContext(context.Background(), query, args...)
}

// QueryRowContext 执行一个预期最多返回一行的查询。
// QueryRowContext 始终返回非 nil 值。错误会被推迟到
// 调用 [Row] 的 Scan 方法时才产生。
// 如果查询没有选中任何行，[*Row.Scan] 将返回 [ErrNoRows]。
// 否则，[*Row.Scan] 扫描第一个选中的行并丢弃
// 其余的行。
func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *Row {
	rows, err := tx.QueryContext(ctx, query, args...)
	return &Row{rows: rows, err: err}
}

// QueryRow 执行一个预期最多返回一行的查询。
// QueryRow 始终返回非 nil 值。错误会被推迟到
// 调用 [Row] 的 Scan 方法时才产生。
// 如果查询没有选中任何行，[*Row.Scan] 将返回 [ErrNoRows]。
// 否则，[*Row.Scan] 扫描第一个选中的行并丢弃
// 其余的行。
//
// QueryRow 内部使用 [context.Background]；要指定 context，请使用
// [Tx.QueryRowContext]。
func (tx *Tx) QueryRow(query string, args ...any) *Row {
	return tx.QueryRowContext(context.Background(), query, args...)
}

// connStmt 是特定连接上的一个已准备语句。
type connStmt struct {
	dc *driverConn
	ds *driverStmt
}

// stmtConnGrabber 表示一个 Tx 或 Conn，它会返回底层的
// driverConn 和 release 函数。
type stmtConnGrabber interface {
	// grabConn 返回 driverConn 以及关联的 release 函数，
	// 操作完成时必须调用该函数。
	grabConn(context.Context) (*driverConn, releaseConn, error)

	// txCtx 返回可用的事务 context。
	// 在等待取消时，应当将返回的 context 与
	// 任何查询 context 一起进行 select。
	txCtx() context.Context
}

var (
	_ stmtConnGrabber = &Tx{}
	_ stmtConnGrabber = &Conn{}
)

// Stmt 是一个已准备的语句。
// Stmt 可以被多个 goroutine 并发安全地使用。
//
// 如果 Stmt 是在 [Tx] 或 [Conn] 上准备的，它将永远绑定到单个
// 底层连接。如果 [Tx] 或 [Conn] 关闭，Stmt 将
// 变得不可用，所有操作都会返回错误。
// 如果 Stmt 是在 [DB] 上准备的，它在 [DB] 的生命周期内
// 保持可用。当 Stmt 需要在一个新的底层连接上执行时，
// 它会自动在新连接上准备自身。
type Stmt struct {
	// 不可变：
	db        *DB    // 我们来自哪里
	query     string // 创建该 Stmt 的查询
	stickyErr error  // 如果非 nil，所有操作都会返回该错误

	closemu sync.RWMutex // 关闭时独占持有，其他时候以读模式持有。

	// 如果 Stmt 是在 Tx 或 Conn 上准备的，则 cg 存在，并且
	// 只会从 cg 获取连接。
	// 如果 cg 为 nil，则 Stmt 必须从 db 获取任意连接，
	// 并通过检查 css 来判断是否必须重新准备该 stmt。
	cg   stmtConnGrabber
	cgds *driverStmt

	// 当从同一 conn 上准备的相同语句请求
	// 特定于事务的语句时，会设置 parentStmt。
	// parentStmt 用于跟踪该语句对
	// 其来源（“父”）语句的依赖，这样用户就可以关闭 parentStmt，
	// 而无需知道是否仍有事务在使用它。
	parentStmt *Stmt

	mu     sync.Mutex // 保护其余字段
	closed bool

	// css 是一个底层驱动语句接口的列表，
	// 这些接口在特定连接上有效。仅在 cg == nil
	// 且找到了一个拥有空闲连接的连接时使用。
	// 如果 cg != nil，则始终使用 cgds。
	css []connStmt

	// lastNumClosed 在创建不带 tx 的 Stmt 时
	// 从 db.numClosed 复制，用于移除 css 中已关闭的连接。
	lastNumClosed uint64
}

// ExecContext 使用给定参数执行一个已准备的语句，
// 并返回一个概述该语句效果的 [Result]。
func (s *Stmt) ExecContext(ctx context.Context, args ...any) (Result, error) {
	s.closemu.RLock()
	defer s.closemu.RUnlock()

	var res Result
	err := s.db.retry(func(strategy connReuseStrategy) error {
		dc, releaseConn, ds, err := s.connStmt(ctx, strategy)
		if err != nil {
			return err
		}

		res, err = resultFromStatement(ctx, dc.ci, ds, args...)
		releaseConn(err)
		return err
	})

	return res, err
}

// Exec 使用给定参数执行一个已准备的语句，
// 并返回一个概述该语句效果的 [Result]。
//
// Exec 内部使用 [context.Background]；要指定 context，请使用
// [Stmt.ExecContext]。
func (s *Stmt) Exec(args ...any) (Result, error) {
	return s.ExecContext(context.Background(), args...)
}

func resultFromStatement(ctx context.Context, ci driver.Conn, ds *driverStmt, args ...any) (Result, error) {
	ds.Lock()
	defer ds.Unlock()

	dargs, err := driverArgsConnLocked(ci, ds, args)
	if err != nil {
		return nil, err
	}

	resi, err := ctxDriverStmtExec(ctx, ds.si, dargs)
	if err != nil {
		return nil, err
	}
	return driverResult{ds.Locker, resi}, nil
}

// removeClosedStmtLocked 移除 s.css 中已关闭的 conn。
//
// 为避免对 DB.mu 的锁争用，我们仅在
// s.db.numClosed - s.lastNum 足够大时才执行。
func (s *Stmt) removeClosedStmtLocked() {
	t := len(s.css)/2 + 1
	if t > 10 {
		t = 10
	}
	dbClosed := s.db.numClosed.Load()
	if dbClosed-s.lastNumClosed < uint64(t) {
		return
	}

	s.db.mu.Lock()
	for i := 0; i < len(s.css); i++ {
		if s.css[i].dc.dbmuClosed {
			s.css[i] = s.css[len(s.css)-1]
			// 在收缩切片之前将最后一个元素置零（以便 GC）。
			s.css[len(s.css)-1] = connStmt{}
			s.css = s.css[:len(s.css)-1]
			i--
		}
	}
	s.db.mu.Unlock()
	s.lastNumClosed = dbClosed
}

// connStmt 返回一个空闲的驱动连接供执行该语句，
// 一个用于释放该连接的函数，以及一个
// 绑定到该连接的语句。
func (s *Stmt) connStmt(ctx context.Context, strategy connReuseStrategy) (dc *driverConn, releaseConn func(error), ds *driverStmt, err error) {
	if err = s.stickyErr; err != nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		err = errors.New("sql: statement is closed")
		return
	}

	// 在事务或连接中，我们始终使用创建该
	// stmt 时所用的连接。
	if s.cg != nil {
		s.mu.Unlock()
		dc, releaseConn, err = s.cg.grabConn(ctx) // 阻塞，等待连接。
		if err != nil {
			return
		}
		return dc, releaseConn, s.cgds, nil
	}

	s.removeClosedStmtLocked()
	s.mu.Unlock()

	dc, err = s.db.conn(ctx, strategy)
	if err != nil {
		return nil, nil, nil, err
	}

	s.mu.Lock()
	for _, v := range s.css {
		if v.dc == dc {
			s.mu.Unlock()
			return dc, dc.releaseConn, v.ds, nil
		}
	}
	s.mu.Unlock()

	// 运气不好；我们需要在此连接上准备该语句
	withLock(dc, func() {
		ds, err = s.prepareOnConnLocked(ctx, dc)
	})
	if err != nil {
		dc.releaseConn(err)
		return nil, nil, nil, err
	}

	return dc, dc.releaseConn, ds, nil
}

// prepareOnConnLocked 在 dc 上准备 Stmt s 中的查询，并将其添加到
// 该语句已打开的 connStmt 列表中。它假定调用方持有 dc 的锁。
func (s *Stmt) prepareOnConnLocked(ctx context.Context, dc *driverConn) (*driverStmt, error) {
	si, err := dc.prepareLocked(ctx, s.cg, s.query)
	if err != nil {
		return nil, err
	}
	cs := connStmt{dc, si}
	s.mu.Lock()
	s.css = append(s.css, cs)
	s.mu.Unlock()
	return cs.ds, nil
}

// QueryContext 使用给定参数执行一个已准备的查询语句，
// 并将查询结果作为 [*Rows] 返回。
func (s *Stmt) QueryContext(ctx context.Context, args ...any) (*Rows, error) {
	s.closemu.RLock()
	defer s.closemu.RUnlock()

	var rowsi driver.Rows
	var rows *Rows

	err := s.db.retry(func(strategy connReuseStrategy) error {
		dc, releaseConn, ds, err := s.connStmt(ctx, strategy)
		if err != nil {
			return err
		}

		rowsi, err = rowsiFromStatement(ctx, dc.ci, ds, args...)
		if err == nil {
			// 注意：ci 的所有权传递给 *Rows，由 releaseConn 负责释放。
			rows = &Rows{
				dc:    dc,
				rowsi: rowsi,
				// releaseConn 在下面设置
			}
			// addDep 必须在 initContextClose 之前添加，否则它可能会
			// 在添加之前就尝试 removeDep。
			s.db.addDep(s, rows)

			// releaseConn 必须在 initContextClose 之前设置，否则它可能会
			// 在设置之前就释放连接。
			rows.releaseConn = func(err error) {
				releaseConn(err)
				s.db.removeDep(s, rows)
			}
			var txctx context.Context
			if s.cg != nil {
				txctx = s.cg.txCtx()
			}
			rows.initContextClose(ctx, txctx)
			return nil
		}

		releaseConn(err)
		return err
	})

	return rows, err
}

// Query 使用给定参数执行一个已准备的查询语句，
// 并将查询结果作为 *Rows 返回。
//
// Query 内部使用 [context.Background]；要指定 context，请使用
// [Stmt.QueryContext]。
func (s *Stmt) Query(args ...any) (*Rows, error) {
	return s.QueryContext(context.Background(), args...)
}

func rowsiFromStatement(ctx context.Context, ci driver.Conn, ds *driverStmt, args ...any) (driver.Rows, error) {
	ds.Lock()
	defer ds.Unlock()
	dargs, err := driverArgsConnLocked(ci, ds, args)
	if err != nil {
		return nil, err
	}
	return ctxDriverStmtQuery(ctx, ds.si, dargs)
}

// QueryRowContext 使用给定参数执行一个已准备的查询语句。
// 如果语句执行期间发生错误，该错误将由对返回的 [*Row]
// 调用 Scan 时返回，而 [*Row] 始终非 nil。
// 如果查询没有选中任何行，[*Row.Scan] 将返回 [ErrNoRows]。
// 否则，[*Row.Scan] 扫描第一个选中的行并丢弃
// 其余的行。
func (s *Stmt) QueryRowContext(ctx context.Context, args ...any) *Row {
	rows, err := s.QueryContext(ctx, args...)
	if err != nil {
		return &Row{err: err}
	}
	return &Row{rows: rows}
}

// QueryRow 使用给定参数执行一个已准备的查询语句。
// 如果语句执行期间发生错误，该错误将由对返回的 [*Row]
// 调用 Scan 时返回，而 [*Row] 始终非 nil。
// 如果查询没有选中任何行，[*Row.Scan] 将返回 [ErrNoRows]。
// 否则，[*Row.Scan] 扫描第一个选中的行并丢弃
// 其余的行。
//
// 用法示例：
//
//	var name string
//	err := nameByUseridStmt.QueryRow(id).Scan(&name)
//
// QueryRow 内部使用 [context.Background]；要指定 context，请使用
// [Stmt.QueryRowContext]。
func (s *Stmt) QueryRow(args ...any) *Row {
	return s.QueryRowContext(context.Background(), args...)
}

// Close 关闭该语句。
func (s *Stmt) Close() error {
	s.closemu.Lock()
	defer s.closemu.Unlock()

	if s.stickyErr != nil {
		return s.stickyErr
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	txds := s.cgds
	s.cgds = nil

	s.mu.Unlock()

	if s.cg == nil {
		return s.db.removeDep(s, s)
	}

	if s.parentStmt != nil {
		// 如果设置了 parentStmt，我们绝不能关闭 s.txds，因为它存储在
		// parentStmt 的 css 数组中。
		return s.db.removeDep(s.parentStmt, s)
	}
	return txds.Close()
}

func (s *Stmt) finalClose() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.css != nil {
		for _, v := range s.css {
			s.db.noteUnusedDriverStatement(v.dc, v.ds)
			v.dc.removeOpenStmt(v.ds)
		}
		s.css = nil
	}
	return nil
}

// Rows 是一次查询的结果。它的游标从结果集的第一行之前
// 开始。使用 [Rows.Next] 逐行前进。
type Rows struct {
	dc          *driverConn // 被持有；关闭时必须调用 releaseConn 来释放
	releaseConn func(error)
	rowsi       driver.Rows
	cancel      func()      // 在 Rows 被关闭时调用，可能为 nil。
	closeStmt   *driverStmt // 如果非 nil，表示关闭时要 Close 的语句

	contextDone atomic.Pointer[error] // awaitDone 看到的错误；在尝试关闭之前设置

	// closemu 防止在有活动的流式结果时关闭 Rows。
	// 它在非关闭操作期间以读模式持有，
	// 在关闭时以独占模式持有。
	//
	// closemu 保护 lasterr 和 closed。
	closemu sync.RWMutex
	lasterr error // 仅当 closed 为 true 时非 nil
	closed  bool

	// closemuScanHold 表示上一次对 Scan 的调用是否让 closemu 保持 RLock
	// 而未解锁。当用户传入 *RawBytes 作为扫描目标时它会这样做。
	// 在这种情况下，我们需要防止 awaitDone 在用户仍在使用
	// 该内存时关闭 Rows。参见 go.dev/issue/60304。
	//
	// 它仅由 Scan、Next 和 NextResultSet 使用，这些函数
	// 不应被并发调用。
	closemuScanHold bool

	// hitEOF 表示 Next 是否在未遇到错误的情况下碰到了行尾。
	// 它在 Next 返回之前设置。它仅由 Next 和 Err 使用，
	// 这些函数不应被并发调用。
	hitEOF bool

	// lastcols 仅在 Scan、Next 和 NextResultSet 中使用，
	// 这些函数不应被并发调用。
	lastcols []driver.Value

	// raw 是 RawBytes 的缓冲区，在多次 Scan 调用之间持续存在。
	// 当驱动返回不匹配的类型而需要克隆分配时使用它。
	// 例如，如果驱动返回 *string，而用户正在扫描到
	// *RawBytes，我们就需要复制该字符串。
	// 这里的 raw 缓冲区让我们可以在多次 Scan 调用之间复用该副本的内存。
	raw []byte
}

// lasterrOrErrLocked 返回 lasterr 或提供的 err。
// rs.closemu 必须处于读锁定状态。
func (rs *Rows) lasterrOrErrLocked(err error) error {
	if rs.lasterr != nil && rs.lasterr != io.EOF {
		return rs.lasterr
	}
	return err
}

// bypassRowsAwaitDone 仅用于测试。
// 如果为 true，则不会由 context 自动关闭 Rows。
var bypassRowsAwaitDone = false

func (rs *Rows) initContextClose(ctx, txctx context.Context) {
	if ctx.Done() == nil && (txctx == nil || txctx.Done() == nil) {
		return
	}
	if bypassRowsAwaitDone {
		return
	}
	closectx, cancel := context.WithCancel(ctx)
	rs.cancel = cancel
	go rs.awaitDone(ctx, txctx, closectx)
}

// awaitDone 阻塞直到 ctx、txctx 或 closectx 被取消。
// ctx 来自查询 context。
// 如果查询是在事务中发起的，事务的 context 也会
// 通过 txctx 提供，以确保在 Tx 关闭时 Rows 被关闭。
// closectx 由对 rs.Close 的显式调用关闭。
func (rs *Rows) awaitDone(ctx, txctx, closectx context.Context) {
	var txctxDone <-chan struct{}
	if txctx != nil {
		txctxDone = txctx.Done()
	}
	select {
	case <-ctx.Done():
		err := ctx.Err()
		rs.contextDone.Store(&err)
	case <-txctxDone:
		err := txctx.Err()
		rs.contextDone.Store(&err)
	case <-closectx.Done():
		// rs.cancel 是通过 Close() 调用的；不要把它存入 contextDone，
		// 以确保 Err() 不受影响。
	}
	rs.close(ctx.Err())
}

// Next 准备下一行结果，以便用 [Rows.Scan] 方法读取。如果成功则返回 true；
// 如果没有下一行结果，或在准备过程中发生错误，则返回 false。[Rows.Err]
// 应被用于区分这两种情况。
//
// 每一次对 [Rows.Scan] 的调用，即使是第一次，都必须先调用 [Rows.Next]。
func (rs *Rows) Next() bool {
	// 如果用户正在调用 Next，说明他们已经用完了上一行的 Scan 结果
	// （任何 RawBytes 内存），因此我们可以释放那个会阻止 awaitDone
	// 调用 close 的读锁。
	rs.closemuRUnlockIfHeldByScan()

	if rs.contextDone.Load() != nil {
		return false
	}

	var doClose, ok bool
	withLock(rs.closemu.RLocker(), func() {
		doClose, ok = rs.nextLocked()
	})
	if doClose {
		rs.Close()
	}
	if doClose && !ok {
		rs.hitEOF = true
	}
	return ok
}

func (rs *Rows) nextLocked() (doClose, ok bool) {
	if rs.closed {
		return false, false
	}

	// 在调用驱动接口 rowsi 之前锁定驱动连接，
	// 以防止 Tx 同时回滚该连接。
	rs.dc.Lock()
	defer rs.dc.Unlock()

	if rs.lastcols == nil {
		rs.lastcols = make([]driver.Value, len(rs.rowsi.Columns()))
	}

	rs.lasterr = rs.rowsi.Next(rs.lastcols)
	if rs.lasterr != nil {
		// 如果有驱动错误，则关闭连接。
		if rs.lasterr != io.EOF {
			return true, false
		}
		nextResultSet, ok := rs.rowsi.(driver.RowsNextResultSet)
		if !ok {
			return true, false
		}
		// 驱动已处于当前结果集的末尾。
		// 测试在当前结果集之后是否还有另一个结果集。
		// 只有在没有更多结果集可读时才关闭 Rows。
		if !nextResultSet.HasNextResultSet() {
			doClose = true
		}
		return doClose, false
	}
	return false, true
}

// NextResultSet 准备下一个结果集以供读取。它报告是否还有更多结果集；
// 如果没有更多结果集，或在推进到下一个结果集时发生错误，则返回 false。
// [Rows.Err] 方法应被用于区分这两种情况。
//
// 在调用 NextResultSet 之后，在扫描之前应始终先调用 [Rows.Next] 方法。
// 如果还有更多结果集，它们可能在结果集中没有行。
func (rs *Rows) NextResultSet() bool {
	// 如果用户正在调用 NextResultSet，说明他们已经用完了上一行的 Scan 结果
	// （任何 RawBytes 内存），因此我们可以释放那个会阻止 awaitDone
	// 调用 close 的读锁。
	rs.closemuRUnlockIfHeldByScan()

	var doClose bool
	defer func() {
		if doClose {
			rs.Close()
		}
	}()
	rs.closemu.RLock()
	defer rs.closemu.RUnlock()

	if rs.closed {
		return false
	}

	rs.lastcols = nil
	nextResultSet, ok := rs.rowsi.(driver.RowsNextResultSet)
	if !ok {
		doClose = true
		return false
	}

	// 在调用驱动接口 rowsi 之前锁定驱动连接，
	// 以防止 Tx 同时回滚该连接。
	rs.dc.Lock()
	defer rs.dc.Unlock()

	rs.lasterr = nextResultSet.NextResultSet()
	if rs.lasterr != nil {
		doClose = true
		return false
	}
	return true
}

// Err 返回在迭代过程中遇到的错误（如果有）。
// 可以在显式或隐式调用 [Rows.Close] 之后调用 Err。
func (rs *Rows) Err() error {
	// 返回行迭代期间可能发生的任何 context 错误，
	// 但仅当我们尚未在行取完后报告最终的 Next() = false 时，
	// 因为在那种情况下用户可能已经在调用 Rows.Err 之前取消了自己的 context。
	if !rs.hitEOF {
		if errp := rs.contextDone.Load(); errp != nil {
			return *errp
		}
	}

	rs.closemu.RLock()
	defer rs.closemu.RUnlock()
	return rs.lasterrOrErrLocked(nil)
}

// rawbuf 返回用于追加 RawBytes 值的缓冲区。
// 该缓冲区在多次调用 Rows.Scan 之间被复用。
//
// 用法：
//
//	rawBytes = rows.setrawbuf(append(rows.rawbuf(), value...))
func (rs *Rows) rawbuf() []byte {
	if rs == nil {
		// convertAssignRows 可以接受 nil 的 *Rows；为简单起见在此处理
		return nil
	}
	return rs.raw
}

// setrawbuf 用向其追加一个新值后的结果更新 RawBytes 缓冲区。
// 它返回新值。
func (rs *Rows) setrawbuf(b []byte) RawBytes {
	if rs == nil {
		// convertAssignRows 可以接受 nil 的 *Rows；为简单起见在此处理
		return RawBytes(b)
	}
	off := len(rs.raw)
	rs.raw = b
	return RawBytes(rs.raw[off:])
}

var errRowsClosed = errors.New("sql: Rows are closed")
var errNoRows = errors.New("sql: no Rows available")

// Columns 返回列名。
// 如果 rows 已关闭，Columns 返回一个错误。
func (rs *Rows) Columns() ([]string, error) {
	rs.closemu.RLock()
	defer rs.closemu.RUnlock()
	if rs.closed {
		return nil, rs.lasterrOrErrLocked(errRowsClosed)
	}
	if rs.rowsi == nil {
		return nil, rs.lasterrOrErrLocked(errNoRows)
	}
	rs.dc.Lock()
	defer rs.dc.Unlock()

	return rs.rowsi.Columns(), nil
}

// ColumnTypes 返回列信息，例如列类型、长度和是否可为 null。
// 某些信息可能无法从某些驱动获取。
func (rs *Rows) ColumnTypes() ([]*ColumnType, error) {
	rs.closemu.RLock()
	defer rs.closemu.RUnlock()
	if rs.closed {
		return nil, rs.lasterrOrErrLocked(errRowsClosed)
	}
	if rs.rowsi == nil {
		return nil, rs.lasterrOrErrLocked(errNoRows)
	}
	rs.dc.Lock()
	defer rs.dc.Unlock()

	return rowsColumnInfoSetupConnLocked(rs.rowsi), nil
}

// ColumnType 包含列的名称和类型。
type ColumnType struct {
	name string

	hasNullable       bool
	hasLength         bool
	hasPrecisionScale bool

	nullable     bool
	length       int64
	databaseType string
	precision    int64
	scale        int64
	scanType     reflect.Type
}

// Name 返回列的名称或别名。
func (ci *ColumnType) Name() string {
	return ci.name
}

// Length 返回可变长度列类型（例如 text 和 binary 字段类型）的列类型长度。
// 如果类型长度无界，则值为 [math.MaxInt64]（任何数据库限制仍将适用）。
// 如果列类型不是可变长度的，例如 int，或如果驱动不支持，则 ok 为 false。
func (ci *ColumnType) Length() (length int64, ok bool) {
	return ci.length, ci.hasLength
}

// DecimalSize 返回 decimal 类型的标度和精度。
// 如果不适用，或如果驱动不支持，则 ok 为 false。
func (ci *ColumnType) DecimalSize() (precision, scale int64, ok bool) {
	return ci.precision, ci.scale, ci.hasPrecisionScale
}

// ScanType 返回一个适合用 [Rows.Scan] 扫描到的 Go 类型。
// 如果驱动不支持此属性，ScanType 将返回空接口的类型。
func (ci *ColumnType) ScanType() reflect.Type {
	return ci.scanType
}

// Nullable 报告该列是否可为 null。
// 如果驱动不支持此属性，ok 将为 false。
func (ci *ColumnType) Nullable() (nullable, ok bool) {
	return ci.nullable, ci.hasNullable
}

// DatabaseTypeName 返回列类型的数据库系统名称。如果返回空字符串，
// 则表示不支持该驱动类型名称。
// 请查阅驱动文档以获取驱动数据类型的列表。[ColumnType.Length]
// 说明符不包含在内。
// 常见的类型名称包括 "VARCHAR"、"TEXT"、"NVARCHAR"、"DECIMAL"、"BOOL"、
// "INT" 和 "BIGINT"。
func (ci *ColumnType) DatabaseTypeName() string {
	return ci.databaseType
}

func rowsColumnInfoSetupConnLocked(rowsi driver.Rows) []*ColumnType {
	names := rowsi.Columns()

	list := make([]*ColumnType, len(names))
	for i := range list {
		ci := &ColumnType{
			name: names[i],
		}
		list[i] = ci

		if prop, ok := rowsi.(driver.RowsColumnTypeScanType); ok {
			ci.scanType = prop.ColumnTypeScanType(i)
		} else {
			ci.scanType = reflect.TypeFor[any]()
		}
		if prop, ok := rowsi.(driver.RowsColumnTypeDatabaseTypeName); ok {
			ci.databaseType = prop.ColumnTypeDatabaseTypeName(i)
		}
		if prop, ok := rowsi.(driver.RowsColumnTypeLength); ok {
			ci.length, ci.hasLength = prop.ColumnTypeLength(i)
		}
		if prop, ok := rowsi.(driver.RowsColumnTypeNullable); ok {
			ci.nullable, ci.hasNullable = prop.ColumnTypeNullable(i)
		}
		if prop, ok := rowsi.(driver.RowsColumnTypePrecisionScale); ok {
			ci.precision, ci.scale, ci.hasPrecisionScale = prop.ColumnTypePrecisionScale(i)
		}
	}
	return list
}

// Scan 将当前行中的列复制到 dest 所指向的值。dest 中的值的数量
// 必须与 [Rows] 中的列数相同。
//
// Scan 将从数据库读取的列转换为以下
// 常见 Go 类型和 sql 包提供的特殊类型：
//
//	*string
//	*[]byte
//	*int, *int8, *int16, *int32, *int64
//	*uint, *uint8, *uint16, *uint32, *uint64
//	*bool
//	*float32, *float64
//	*interface{}
//	*RawBytes
//	*Rows (cursor value)
//	any type implementing Scanner (see Scanner docs)
//
// 在最简单的情况下，如果源列值的类型为整数、bool 或字符串类型 T，
// 而 dest 的类型为 *T，Scan 只需通过指针赋值。
//
// 只要不丢失信息，Scan 也可以在字符串和数值类型之间转换。虽然 Scan 会将
// 从数值数据库列扫描到的所有数字字符串化写入 *string，但扫描到
// 数值类型时会检查溢出。例如，值为 300 的 float64 或值为 "300" 的字符串
// 可以扫描进 uint16，但不能扫描进 uint8，尽管 float64(255) 或 "255"
// 可以扫描进 uint8。一个例外是，某些 float64 数字扫描到字符串时
// 在字符串化过程中可能丢失信息。一般而言，应将浮点列扫描进 *float64。
//
// 如果某个 dest 参数的类型为 *[]byte，Scan 会在该参数中保存
// 相应数据的副本。该副本归调用者所有，
// 可以被修改并无限期持有。可以通过改用类型为 [*RawBytes] 的参数
// 来避免复制；关于其使用的限制，请参阅 [RawBytes] 的文档。
//
// 如果某个参数的类型为 *interface{}，Scan 会不经转换地复制
// 底层驱动提供的值。当从类型为 []byte 的源值扫描到 *interface{} 时，
// 会制作该切片的一个副本，结果归调用者所有。
//
// 类型为 [time.Time] 的源值可以被扫描进类型为
// *time.Time、*interface{}、*string 或 *[]byte 的值。当转换为
// 后两者时，使用 [time.RFC3339Nano]。
//
// 类型为 bool 的源值可以被扫描进类型 *bool、
// *interface{}、*string、*[]byte 或 [*RawBytes]。
//
// 对于扫描进 *bool，源可以是 true、false、1、0，或
// 可被 [strconv.ParseBool] 解析的字符串输入。
//
// Scan 还可以将查询返回的游标（例如
// "select cursor(select * from my_table) from dual"）转换为
// 一个 [*Rows] 值，该值本身可以被扫描。如果父 [*Rows] 被关闭，
// 父 select 查询将关闭任何游标 [*Rows]。
//
// 如果任何实现 [Scanner] 的前导参数返回错误，
// 该错误将被包装在返回的错误中。
func (rs *Rows) Scan(dest ...any) error {
	if rs.closemuScanHold {
		// 只有在用户连续调用 Scan 两次而不调用 Next 时才可能出现这种情况。
		return fmt.Errorf("sql: Scan called without calling Next (closemuScanHold)")
	}

	rs.closemu.RLock()
	rs.raw = rs.raw[:0]
	err := rs.scanLocked(dest...)
	if err == nil && scanArgsContainRawBytes(dest) {
		rs.closemuScanHold = true
	} else {
		rs.closemu.RUnlock()
	}
	return err
}

func (rs *Rows) scanLocked(dest ...any) error {
	if rs.lasterr != nil && rs.lasterr != io.EOF {
		return rs.lasterr
	}
	if rs.closed {
		return rs.lasterrOrErrLocked(errRowsClosed)
	}

	if rs.lastcols == nil {
		return errors.New("sql: Scan called without calling Next")
	}
	if len(dest) != len(rs.lastcols) {
		return fmt.Errorf("sql: expected %d destination arguments in Scan, not %d", len(rs.lastcols), len(dest))
	}

	for i, sv := range rs.lastcols {
		err := convertAssignRows(dest[i], sv, rs)
		if err != nil {
			return fmt.Errorf(`sql: Scan error on column index %d, name %q: %w`, i, rs.rowsi.Columns()[i], err)
		}
	}
	return nil
}

// closemuRUnlockIfHeldByScan 释放先前使用 *RawBytes 调用 Scan 时
// 持有并保持打开的 closemu.RLock。
func (rs *Rows) closemuRUnlockIfHeldByScan() {
	if rs.closemuScanHold {
		rs.closemuScanHold = false
		rs.closemu.RUnlock()
	}
}

func scanArgsContainRawBytes(args []any) bool {
	for _, a := range args {
		if _, ok := a.(*RawBytes); ok {
			return true
		}
	}
	return false
}

// rowsCloseHook 返回一个函数，以便测试可以通过仅测试用的互斥锁
// 安装该钩子。
var rowsCloseHook = func() func(*Rows, *error) { return nil }

// Close 关闭 [Rows]，阻止进一步枚举。如果调用 [Rows.Next]
// 并返回 false 且没有更多结果集，[Rows] 会自动关闭，
// 此时只需检查 [Rows.Err] 的结果即可。Close 是幂等的，
// 且不影响 [Rows.Err] 的结果。
func (rs *Rows) Close() error {
	// 如果用户正在调用 Close，说明他们已经用完了上一行的 Scan 结果
	// （任何 RawBytes 内存），因此我们可以释放那个会阻止 awaitDone
	// 在我们执行之前调用未导出的 close 的读锁。
	rs.closemuRUnlockIfHeldByScan()

	return rs.close(nil)
}

func (rs *Rows) close(err error) error {
	rs.closemu.Lock()
	defer rs.closemu.Unlock()

	if rs.closed {
		return nil
	}
	rs.closed = true

	if rs.lasterr == nil {
		rs.lasterr = err
	}

	withLock(rs.dc, func() {
		err = rs.rowsi.Close()
	})
	if fn := rowsCloseHook(); fn != nil {
		fn(rs, &err)
	}
	if rs.cancel != nil {
		rs.cancel()
	}

	if rs.closeStmt != nil {
		rs.closeStmt.Close()
	}
	rs.releaseConn(err)

	rs.lasterr = rs.lasterrOrErrLocked(err)
	return err
}

// Row 是调用 [DB.QueryRow] 以选择单行的结果。
type Row struct {
	// 以下两者之一将为非 nil：
	err  error // 为便于链式调用而延迟的错误
	rows *Rows
}

// Scan 将匹配行中的列复制到 dest 所指向的值。
// 详情请参阅 [Rows.Scan] 的文档。
// 如果有多行匹配该查询，Scan 使用第一行并丢弃其余的。
// 如果没有行匹配该查询，Scan 返回 [ErrNoRows]。
func (r *Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}

	// TODO(bradfitz)：目前我们需要防御性地克隆驱动返回的所有
	// []byte（不允许在 Rows.Scan 中使用 *RawBytes），因为当我们
	// 从这个函数返回时，我们的 defer 即将关闭 Rows。
	// 与驱动 Next(...) 接口的约定是，它可以返回指向只读临时内存的切片，
	// 该内存仅在下次 Scan/Close 之前有效。但 TODO 是，
	// 对于很多驱动而言，这种复制是不必要的。我们应该提供一个可选的
	// 接口供驱动实现，以表明“别担心，我从 Next 返回的 []byte
	// 不会被再次修改。”（例如，如果它们本来就是从网络获取的）
	// 但目前我们不在意。
	defer r.rows.Close()
	if scanArgsContainRawBytes(dest) {
		return errors.New("sql: RawBytes isn't allowed on Row.Scan")
	}

	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return ErrNoRows
	}
	err := r.rows.Scan(dest...)
	if err != nil {
		return err
	}
	// 确保查询能够在没有错误的情况下完整处理。
	return r.rows.Close()
}

// Err 为包装包提供了一种在不调用 [Row.Scan] 的情况下检查
// 查询错误的方式。
// Err 返回在运行查询时遇到的错误（如果有）。
// 如果此错误不为 nil，则 [Row.Scan] 也会返回此错误。
func (r *Row) Err() error {
	return r.err
}

// Result 汇总了一条已执行的 SQL 命令。
type Result interface {
	// LastInsertId 返回数据库为响应命令而生成的整数。
	// 通常这来自插入新行时的 "auto increment" 列。
	// 并非所有数据库都支持此特性，且此类语句的语法各不相同。
	LastInsertId() (int64, error)

	// RowsAffected 返回受 update、insert 或 delete 影响的行数。
	// 并非每个数据库或数据库驱动都支持此功能。
	RowsAffected() (int64, error)
}

type driverResult struct {
	sync.Locker // 即 *driverConn
	resi        driver.Result
}

func (dr driverResult) LastInsertId() (int64, error) {
	dr.Lock()
	defer dr.Unlock()
	return dr.resi.LastInsertId()
}

func (dr driverResult) RowsAffected() (int64, error) {
	dr.Lock()
	defer dr.Unlock()
	return dr.resi.RowsAffected()
}

func stack() string {
	var buf [2 << 10]byte
	return string(buf[:runtime.Stack(buf[:], false)])
}

// withLock 在持有 lk 的情况下运行。
func withLock(lk sync.Locker, fn func()) {
	lk.Lock()
	defer lk.Unlock() // 以防 fn 发生 panic
	fn()
}

// connRequestSet 是一组 chan connRequest，它针对以下操作
// 进行了优化：
//
//   - 添加元素
//   - 移除元素（只能由添加它的调用者执行）
//   - 取出（获取 + 删除）一个随机元素
//
// 我们之前为此使用了 map，但取出随机元素代价高昂，
// 需要用到 map 迭代器。该类型完全避免使用 map，
// 只使用一个切片。
type connRequestSet struct {
	// s 是集合中的元素。
	s []connRequestAndIndex
}

type connRequestAndIndex struct {
	// req 是集合中的元素。
	req chan connRequest

	// curIdx 指向该元素在
	// connRequestSet.s 中的当前位置。移除时它会被设为 -1。
	curIdx *int
}

// CloseAndRemoveAll 关闭集合中的所有 channel
// 并清空该集合。
func (s *connRequestSet) CloseAndRemoveAll() {
	for _, v := range s.s {
		*v.curIdx = -1
		close(v.req)
	}
	s.s = nil
}

// Len 返回集合的长度。
func (s *connRequestSet) Len() int { return len(s.s) }

// connRequestDelHandle 是一个不透明的句柄，用于删除
// 由 Add 调用所添加的项。
type connRequestDelHandle struct {
	idx *int // 指向索引的指针；如果不在切片中则为 -1
}

// Add 将 v 添加到等待请求的集合中。
// 返回的 connRequestDelHandle 可用于从集合中
// 移除该项。
func (s *connRequestSet) Add(v chan connRequest) connRequestDelHandle {
	idx := len(s.s)
	// TODO(bradfitz)：为简单起见，这里总是分配一个新的 int 大小的
	// 分配来存储索引。但一般而言集合会很小，
	// 处于可扫描的阈值以下。作为一种优化，我们可以在集合很小且
	// 应被扫描时允许 *int 为 nil。即使集合在存在未处理删除句柄的情况下
	// 增长超过阈值，这也能工作，因为元素只会移动到更低的索引。
	// 所以如果它开始时为 nil 位置，它将始终处于低索引因而可被扫描。
	// 但这可以在后续变更中完成。
	idxPtr := &idx
	s.s = append(s.s, connRequestAndIndex{v, idxPtr})
	return connRequestDelHandle{idxPtr}
}

// Delete 从集合中移除一个元素。
//
// 它报告该元素是否被删除。（如果 TakeRandom 的某个调用者
// 在此期间将它取走，或者第二次调用 Delete，则可能返回 false）
func (s *connRequestSet) Delete(h connRequestDelHandle) bool {
	idx := *h.idx
	if idx < 0 {
		return false
	}
	s.deleteIndex(idx)
	return true
}

func (s *connRequestSet) deleteIndex(idx int) {
	// 将该项标记为已删除。
	*(s.s[idx].curIdx) = -1
	// 复制最后一个元素，将其位置
	// 更新到它的新位置。
	if idx < len(s.s)-1 {
		last := s.s[len(s.s)-1]
		*last.curIdx = idx
		s.s[idx] = last
	}
	// 在收缩切片之前将最后一个元素清零（以便 GC）。
	s.s[len(s.s)-1] = connRequestAndIndex{}
	s.s = s.s[:len(s.s)-1]
}

// TakeRandom 从 s 中返回并移除一个随机元素，
// 并报告是否有可取的元素。（如果集合为空，
// 则返回 ok=false。）
func (s *connRequestSet) TakeRandom() (v chan connRequest, ok bool) {
	if len(s.s) == 0 {
		return nil, false
	}
	pick := rand.IntN(len(s.s))
	e := s.s[pick]
	s.deleteIndex(pick)
	return e.req, true
}
