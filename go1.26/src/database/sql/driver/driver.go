// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package driver 定义了由数据库驱动实现的接口，
// 供 package sql 使用。
//
// 大多数代码应使用 [database/sql] 包。
//
// driver 接口随时间演变。驱动应实现
// [Connector] 和 [DriverContext] 接口。
// Connector.Connect 和 Driver.Open 方法永远不应返回 [ErrBadConn]。
// 只有当连接已处于无效（例如已关闭）状态时，才应从 [Validator]、[SessionResetter]
// 或查询方法返回 [ErrBadConn]。
//
// 所有 [Conn] 实现都应实现以下接口：
// [Pinger]、[SessionResetter] 和 [Validator]。
//
// 如果支持命名参数或 context，驱动的 [Conn] 应实现：
// [ExecerContext]、[QueryerContext]、[ConnPrepareContext] 和 [ConnBeginTx]。
//
// 要支持自定义数据类型，请实现 [NamedValueChecker]。[NamedValueChecker]
// 还允许查询通过从 CheckNamedValue 返回
// [ErrRemoveArgument] 来接受每次查询的选项作为参数。
//
// 如果支持多个结果集，[Rows] 应实现 [RowsNextResultSet]。
// 如果驱动知道如何描述返回结果中存在的类型，
// 它应实现以下接口： [RowsColumnTypeScanType]、
// [RowsColumnTypeDatabaseTypeName]、[RowsColumnTypeLength]、[RowsColumnTypeNullable]、
// 和 [RowsColumnTypePrecisionScale]。给定的行值也可能返回一个 [Rows]
// 类型，它可以表示数据库游标值。
//
// 如果某个 [Conn] 实现了 [Validator]，那么在将连接归还到连接池
// 之前会调用 IsValid 方法。如果连接池中的某个条目实现了 [SessionResetter]，
// 那么在将该连接重用于另一次查询之前会调用 ResetSession。
// 如果某个连接从未归还到连接池而是被立即重用，那么在重用之前
// 会调用 ResetSession，但不会调用 IsValid。
package driver

import (
	"context"
	"errors"
	"reflect"
)

// Value 是驱动必须能够处理的值。
// 它要么是 nil、由数据库驱动的 [NamedValueChecker]
// 接口处理的类型，要么是以下类型之一的实例：
//
//	int64
//	float64
//	bool
//	[]byte
//	string
//	time.Time
//
// 如果驱动支持游标，返回的 Value 还可以实现本包中的 [Rows]
// 接口。例如，当用户选择一个游标（如 "select cursor(select * from my_table) from dual"）时
// 就会用到它。如果该 select 的 [Rows]
// 被关闭，游标 [Rows] 也将被关闭。
type Value any

// NamedValue 同时保存值的名称和值。
type NamedValue struct {
	// 如果 Name 非空，应将其用于参数标识符，
	// 而不是序号位置。
	//
	// Name 不会有符号前缀。
	Name string

	// 参数的序号位置，从 1 开始且总是会被设置。
	Ordinal int

	// Value 是参数值。
	Value Value
}

// Driver 是数据库驱动必须实现的接口。
//
// 数据库驱动可以实现 [DriverContext]，以便访问
// context，并且只为一组连接解析一次名称，
// 而不是每个连接解析一次。
type Driver interface {
	// Open 返回一个到数据库的新连接。
	// name 是一个驱动特定格式的字符串。
	//
	// Open 可以返回一个缓存的连接（之前关闭过的），
	// 但这样做没有必要；sql 包
	// 维护了一个空闲连接池以便高效复用。
	//
	// 返回的连接同一时间只会被一个 goroutine
	// 使用。
	Open(name string) (Conn, error)
}

// 如果某个 [Driver] 实现了 DriverContext，那么 [database/sql.DB] 将调用
// OpenConnector 获取一个 [Connector]，然后调用
// 该 [Connector] 的 Connect 方法获取每个所需的连接，
// 而不是为每个连接调用 [Driver] 的 Open 方法。
// 这个两步序列让驱动只需解析一次名称，
// 并且还提供了对每个 [Conn] 的 context 的访问。
type DriverContext interface {
	// OpenConnector 必须以与 Driver.Open 解析 name 参数
	// 相同的格式解析 name。
	OpenConnector(name string) (Connector, error)
}

// Connector 表示处于固定配置中的驱动，
// 可以创建任意数量的等价 Conn 供多个 goroutine
// 使用。
//
// Connector 可以传递给 [database/sql.OpenDB]，以允许驱动
// 实现自己的 [database/sql.DB] 构造器，或者由
// [DriverContext] 的 OpenConnector 方法返回，以允许驱动
// 访问 context 并避免重复解析驱动
// 配置。
//
// 如果 Connector 实现了 [io.Closer]，[database/sql.DB.Close]
// 方法将调用 Close 方法并返回错误（如果有）。
type Connector interface {
	// Connect 返回一个到数据库的连接。
	// Connect 可以返回一个缓存的连接（之前关闭过的），
	// 但这样做没有必要；sql 包
	// 维护了一个空闲连接池以便高效复用。
	//
	// 提供的 context.Context 仅用于拨号目的
	// （参见 net.DialContext），不应被存储或用于
	// 其他目的。拨号时仍应使用默认超时，
	// 因为连接池可能会在对任何查询
	// 异步地调用 Connect。
	//
	// 返回的连接同一时间只会被一个 goroutine
	// 使用。
	Connect(context.Context) (Conn, error)

	// Driver 返回 Connector 底层的 Driver，
	// 主要是为了保持与 sql.DB 上
	// Driver 方法的兼容性。
	Driver() Driver
}

// ErrSkip 可能由某些可选接口的方法返回，以
// 在运行时表明快速路径不可用，sql
// 包应继续执行，就像该可选接口未被
// 实现一样。ErrSkip 仅在明确
// 记录的地方受支持。
var ErrSkip = errors.New("driver: skip fast-path; continue as if unimplemented")

// ErrBadConn 应由驱动返回，以向 [database/sql]
// 包表明某个 driver.[Conn] 处于不良状态（例如服务器
// 之前已关闭了连接），并且 [database/sql] 包应
// 在一个新连接上重试。
//
// 为防止重复操作，如果数据库服务器有可能
// 已经执行了该操作，就不应返回 ErrBadConn。
// 即使服务器返回了一个错误，
// 你也不应返回 ErrBadConn。
//
// 错误将使用 [errors.Is] 进行检查。错误可以
// 包装 ErrBadConn 或实现 Is(error) bool 方法。
var ErrBadConn = errors.New("driver: bad connection")

// Pinger 是一个可选接口，可以由 [Conn] 实现。
//
// 如果 [Conn] 未实现 Pinger，[database/sql.DB.Ping] 和
// [database/sql.DB.PingContext] 将检查是否至少有一个 [Conn] 可用。
//
// 如果 Conn.Ping 返回 [ErrBadConn]，[database/sql.DB.Ping] 和 [database/sql.DB.PingContext] 将
// 从连接池中移除该 [Conn]。
type Pinger interface {
	Ping(ctx context.Context) error
}

// Execer 是一个可选接口，可以由 [Conn] 实现。
//
// 如果 [Conn] 既不实现 [ExecerContext] 也不实现 [Execer]，
// [database/sql.DB.Exec] 将先准备一个查询，执行该语句，
// 然后关闭该语句。
//
// Exec 可以返回 [ErrSkip]。
//
// Deprecated: 驱动应改为实现 [ExecerContext]。
type Execer interface {
	Exec(query string, args []Value) (Result, error)
}

// ExecerContext 是一个可选接口，可以由 [Conn] 实现。
//
// 如果 [Conn] 未实现 [ExecerContext]，[database/sql.DB.Exec]
// 将回退到 [Execer]；如果 Conn 也未实现 Execer，
// [database/sql.DB.Exec] 将先准备一个查询，执行该语句，然后
// 关闭该语句。
//
// ExecContext 可以返回 [ErrSkip]。
//
// ExecContext 必须遵守 context 超时，并在 context 被取消时返回。
type ExecerContext interface {
	ExecContext(ctx context.Context, query string, args []NamedValue) (Result, error)
}

// Queryer 是一个可选接口，可以由 [Conn] 实现。
//
// 如果 [Conn] 既不实现 [QueryerContext] 也不实现 [Queryer]，
// [database/sql.DB.Query] 将先准备一个查询，执行该语句，
// 然后关闭该语句。
//
// Query 可以返回 [ErrSkip]。
//
// Deprecated: 驱动应改为实现 [QueryerContext]。
type Queryer interface {
	Query(query string, args []Value) (Rows, error)
}

// QueryerContext 是一个可选接口，可以由 [Conn] 实现。
//
// 如果 [Conn] 未实现 QueryerContext，[database/sql.DB.Query]
// 将回退到 [Queryer]；如果 [Conn] 也未实现 [Queryer]，
// [database/sql.DB.Query] 将先准备一个查询，执行该语句，然后
// 关闭该语句。
//
// QueryContext 可以返回 [ErrSkip]。
//
// QueryContext 必须遵守 context 超时，并在 context 被取消时返回。
type QueryerContext interface {
	QueryContext(ctx context.Context, query string, args []NamedValue) (Rows, error)
}

// Conn 是一个到数据库的连接。它不会被多个
// goroutine 并发使用。
//
// Conn 被认为是有状态的。
type Conn interface {
	// Prepare 返回一个绑定到该连接的预处理语句。
	Prepare(query string) (Stmt, error)

	// Close 使任何当前的预处理语句和事务失效并可能停止它们，
	// 将此连接标记为不再使用。
	//
	// 因为 sql 包维护了一个空闲连接池，
	// 且只在空闲连接过剩时才调用 Close，
	// 所以驱动不必自己做连接缓存。
	//
	// 驱动必须确保 Close 发起的所有网络调用
	// 不会无限期阻塞（例如应用超时）。
	Close() error

	// Begin 启动并返回一个新事务。
	//
	// Deprecated: 驱动应改为（或额外）实现 ConnBeginTx。
	Begin() (Tx, error)
}

// ConnPrepareContext 用 context 增强了 [Conn] 接口。
type ConnPrepareContext interface {
	// PrepareContext 返回一个绑定到该连接的预处理语句。
	// context 用于语句的准备阶段，
	// 它不得将 context 存储在语句本身中。
	PrepareContext(ctx context.Context, query string) (Stmt, error)
}

// IsolationLevel 是存储在 [TxOptions] 中的事务隔离级别。
//
// 该类型应与 [database/sql.IsolationLevel]
// 以及其定义的任何值视为相同。
type IsolationLevel int

// TxOptions 保存事务选项。
//
// 该类型应与 [database/sql.TxOptions] 视为相同。
type TxOptions struct {
	Isolation IsolationLevel
	ReadOnly  bool
}

// ConnBeginTx 用 context 和 [TxOptions] 增强了 [Conn] 接口。
type ConnBeginTx interface {
	// BeginTx 启动并返回一个新事务。
	// 如果用户取消了 context，sql 包将在
	// 丢弃并关闭连接之前调用 Tx.Rollback。
	//
	// 这必须检查 opts.Isolation 以确定是否设置了
	// 隔离级别。如果驱动不支持非默认
	// 级别且设置了该级别，或者存在一个驱动
	// 不支持的非默认隔离级别，则必须返回错误。
	//
	// 这还必须检查 opts.ReadOnly 以确定只读
	// 值是否为 true，从而在支持时设置只读事务属性，
	// 或者在不支持时返回错误。
	BeginTx(ctx context.Context, opts TxOptions) (Tx, error)
}

// SessionResetter 可以由 [Conn] 实现，以允许驱动重置
// 与连接关联的会话状态并标记不良连接。
type SessionResetter interface {
	// 如果连接之前被使用过，则在连接上执行查询之前
	// 会调用 ResetSession。如果驱动返回 ErrBadConn，
	// 则丢弃该连接。
	ResetSession(ctx context.Context) error
}

// Validator 可以由 [Conn] 实现，以允许驱动
// 标记一个连接是否有效或是否应被丢弃。
//
// 如果实现了，驱动可以返回查询的底层错误，
// 即使该连接应被连接池丢弃。
type Validator interface {
	// 在将连接放入连接池之前会调用 IsValid。
	// 如果返回 false，则丢弃该连接。
	IsValid() bool
}

// Result 是查询执行的结果。
type Result interface {
	// LastInsertId 返回数据库自动生成的 ID，
	// 例如在向带主键的表执行 INSERT 之后。
	LastInsertId() (int64, error)

	// RowsAffected 返回查询影响的行数。
	RowsAffected() (int64, error)
}

// Stmt 是一个预处理语句。它绑定到一个 [Conn]，且不会被
// 多个 goroutine 并发使用。
type Stmt interface {
	// Close 关闭该语句。
	//
	// 从 Go 1.1 起，如果 Stmt 正被任何查询使用，
	// 它不会被关闭。
	//
	// 驱动必须确保 Close 发起的所有网络调用
	// 不会无限期阻塞（例如应用超时）。
	Close() error

	// NumInput 返回占位符参数的数量。
	//
	// 如果 NumInput 返回 >= 0，sql 包将在调用该语句的
	// Exec 或 Query 方法之前，对来自调用方的参数数量
	// 进行合理性检查，并向调用方返回错误。
	//
	// 如果驱动不知道其占位符数量，NumInput 也可能
	// 返回 -1。在这种情况下，sql 包将不会对 Exec 或 Query
	// 的参数数量做合理性检查。
	NumInput() int

	// Exec 执行一个不返回行的查询，例如
	// INSERT 或 UPDATE。
	//
	// Deprecated: 驱动应改为（或额外）实现 StmtExecContext。
	Exec(args []Value) (Result, error)

	// Query 执行一个可能返回行的查询，例如
	// SELECT。
	//
	// Deprecated: 驱动应改为（或额外）实现 StmtQueryContext。
	Query(args []Value) (Rows, error)
}

// StmtExecContext 通过为 Exec 提供 context 来增强 [Stmt] 接口。
type StmtExecContext interface {
	// ExecContext 执行一个不返回行的查询，
	// 例如 INSERT 或 UPDATE。
	//
	// ExecContext 必须遵守 context 超时，并在其被取消时返回。
	ExecContext(ctx context.Context, args []NamedValue) (Result, error)
}

// StmtQueryContext 通过为 Query 提供 context 来增强 [Stmt] 接口。
type StmtQueryContext interface {
	// QueryContext 执行一个可能返回行的查询，例如
	// SELECT。
	//
	// QueryContext 必须遵守 context 超时，并在其被取消时返回。
	QueryContext(ctx context.Context, args []NamedValue) (Rows, error)
}

// ErrRemoveArgument 可以从 [NamedValueChecker] 返回，以指示
// [database/sql] 包不要将该参数传递给驱动查询接口。
// 在接收并非 SQL 查询参数的特定于查询的选项或结构时返回。
var ErrRemoveArgument = errors.New("driver: remove argument from query")

// NamedValueChecker 可以由 [Conn] 或 [Stmt] 可选地实现。它为
// 驱动提供更多的控制，以处理默认允许的 [Value] 类型之外的
// Go 类型和数据库类型。
//
// [database/sql] 包按以下顺序检查值检查器，
// 停在第一个找到的匹配项：Stmt.NamedValueChecker、Conn.NamedValueChecker、
// Stmt.ColumnConverter、[DefaultParameterConverter]。
//
// 如果 CheckNamedValue 返回 [ErrRemoveArgument]，则该 [NamedValue] 将不会包含在
// 最终查询参数中。这可以用来向查询本身
// 传递特殊选项。
//
// 如果返回 [ErrSkip]，则该参数将使用列转换器的错误检查
// 路径。驱动可能希望在其自身的特殊情形
// 用尽后返回 [ErrSkip]。
type NamedValueChecker interface {
	// CheckNamedValue 在将参数传递给驱动之前被调用，
	// 并替代任何 ColumnConverter。CheckNamedValue 必须
	// 按驱动的适当方式做类型验证和转换。
	CheckNamedValue(*NamedValue) error
}

// ColumnConverter 可以由 [Stmt] 可选地实现，前提是该语句
// 知道自己各列的类型，并能够从任何类型转换为
// driver [Value]。
//
// Deprecated: 驱动应改为实现 [NamedValueChecker]。
type ColumnConverter interface {
	// ColumnConverter 为提供的列索引返回一个 ValueConverter。
	// 如果某个特定列的类型未知或不应被特殊处理，
	// 可以返回 [DefaultParameterConverter]。
	ColumnConverter(idx int) ValueConverter
}

// Rows 是对已执行查询结果的迭代器。
type Rows interface {
	// Columns 返回列的名称。结果的列数
	// 由切片长度推断。如果某个特定的列名
	// 未知，则该条目应返回空字符串。
	Columns() []string

	// Close 关闭 rows 迭代器。
	Close() error

	// 调用 Next 将下一行数据填充到
	// 提供的切片中。提供的切片大小将与
	// Columns() 的宽度相同。
	//
	// 当没有更多行时，Next 应返回 io.EOF。
	//
	// 不应在 Next 之外写入 dest。关闭 Rows 时
	// 应注意不要修改 dest 中持有的
	// 缓冲区。
	Next(dest []Value) error
}

// RowsNextResultSet 通过提供一种方式，让驱动前进到下一个
// 结果集，来扩展 [Rows] 接口。
type RowsNextResultSet interface {
	Rows

	// 在当前结果集结束时调用 HasNextResultSet，
	// 并报告在当前结果集之后是否还有另一个结果集。
	HasNextResultSet() bool

	// NextResultSet 让驱动前进到下一个结果集，即使
	// 当前结果集中还有剩余的行。
	//
	// 当没有更多结果集时，NextResultSet 应返回 io.EOF。
	NextResultSet() error
}

// RowsColumnTypeScanType 可以由 [Rows] 实现。它应返回
// 可用于扫描类型的值类型。例如，对于数据库
// 列类型 "bigint"，它应返回 "[reflect.TypeOf](int64(0))"。
type RowsColumnTypeScanType interface {
	Rows
	ColumnTypeScanType(index int) reflect.Type
}

// RowsColumnTypeDatabaseTypeName 可以由 [Rows] 实现。它应返回
// 不带长度的数据库系统类型名。类型名应为大写。
// 返回类型的示例："VARCHAR"、"NVARCHAR"、"VARCHAR2"、"CHAR"、"TEXT"、
// "DECIMAL"、"SMALLINT"、"INT"、"BIGINT"、"BOOL"、"[]BIGINT"、"JSONB"、"XML"、
// "TIMESTAMP"。
type RowsColumnTypeDatabaseTypeName interface {
	Rows
	ColumnTypeDatabaseTypeName(index int) string
}

// RowsColumnTypeLength 可以由 [Rows] 实现。如果该列是
// 变长类型，它应返回列类型的长度。如果该列
// 不是变长类型，ok 应返回 false。
// 如果长度除系统限制外不受限制，它应返回 [math.MaxInt64]。
// 以下是各种类型返回值示例：
//
//	TEXT          (math.MaxInt64, true)
//	varchar(10)   (10, true)
//	nvarchar(10)  (10, true)
//	decimal       (0, false)
//	int           (0, false)
//	bytea(30)     (30, true)
type RowsColumnTypeLength interface {
	Rows
	ColumnTypeLength(index int) (length int64, ok bool)
}

// RowsColumnTypeNullable 可以由 [Rows] 实现。如果已知该列可能为 null，
// 则 nullable 值应为 true；如果已知该列不可为 null，
// 则为 false。
// 如果列的 null 属性未知，ok 应为 false。
type RowsColumnTypeNullable interface {
	Rows
	ColumnTypeNullable(index int) (nullable, ok bool)
}

// RowsColumnTypePrecisionScale 可以由 [Rows] 实现。它应返回
// decimal 类型的精度和小数位数。如果不适用，ok 应为 false。
// 以下是各种类型返回值示例：
//
//	decimal(38, 4)    (38, 4, true)
//	int               (0, 0, false)
//	decimal           (math.MaxInt64, math.MaxInt64, true)
type RowsColumnTypePrecisionScale interface {
	Rows
	ColumnTypePrecisionScale(index int) (precision, scale int64, ok bool)
}

// Tx 是一个事务。
type Tx interface {
	Commit() error
	Rollback() error
}

// RowsAffected 为 INSERT 或 UPDATE 操作实现 [Result]，
// 该操作会改变若干行。
type RowsAffected int64

var _ Result = RowsAffected(0)

func (RowsAffected) LastInsertId() (int64, error) {
	return 0, errors.New("LastInsertId is not supported by this driver")
}

func (v RowsAffected) RowsAffected() (int64, error) {
	return int64(v), nil
}

// ResultNoRows 是一个预定义的 [Result]，供驱动在 DDL
// 命令（如 CREATE TABLE）成功时返回。它对于 LastInsertId
// 和 [RowsAffected] 都会返回错误。
var ResultNoRows noRows

type noRows struct{}

var _ Result = noRows{}

func (noRows) LastInsertId() (int64, error) {
	return 0, errors.New("no LastInsertId available after DDL statement")
}

func (noRows) RowsAffected() (int64, error) {
	return 0, errors.New("no RowsAffected available after DDL statement")
}
