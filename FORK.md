# Fork notes

`github.com/playneta/gocqlx/v3` is a fork of [scylladb/gocqlx](https://github.com/scylladb/gocqlx)
**v3.0.4**, retargeted from the ScyllaDB gocql fork to the Apache driver
[`github.com/apache/cassandra-gocql-driver/v2`](https://github.com/apache/cassandra-gocql-driver) **v2.1.2**.

## Why a fork is necessary

`replace github.com/gocql/gocql => github.com/apache/cassandra-gocql-driver/v2` does not work:

```
go: github.com/apache/cassandra-gocql-driver/v2@v2.1.2 used for two different module paths
    (github.com/apache/cassandra-gocql-driver/v2 and github.com/gocql/gocql)
```

The Apache driver imports its own `internal/lru`, `internal/murmur` and `internal/streams`
packages under its real module path, so it cannot simultaneously answer to
`github.com/gocql/gocql`. Since every gocqlx release hard-codes that old import path,
rewriting the imports in a fork is the only way to combine the two.

Upstream tracking issue: [apache/cassandra-gocql-driver#1905](https://github.com/apache/cassandra-gocql-driver/issues/1905).

## Changes from upstream v3.0.4

Beyond the mechanical import rewrite (`github.com/gocql/gocql` →
`github.com/apache/cassandra-gocql-driver/v2`, `github.com/scylladb/gocqlx/v3` →
`github.com/playneta/gocqlx/v3`):

### `queryx.go`, `batchx.go` — removed pooling and ScyllaDB-only methods

- Dropped `defer q.Release()` from `ExecRelease`, `ExecCASRelease`, `GetRelease`,
  `GetCASRelease` and `SelectRelease`. gocql v2 removed query pooling and with it
  `Query.Release()`. The five methods are kept so callers still compile, but they are
  now plain aliases for `Exec`/`Get`/`Select` and are marked deprecated.
- Removed `Queryx.GetRequestTimeout`, `Queryx.SetRequestTimeout`, `Queryx.SetHostID`
  and the `Batch` equivalents. These wrap ScyllaDB-fork-only driver methods that the
  Apache driver does not have.

### `migrate/migrate.go`, `dbutil/rewrite.go`

- Dropped the two remaining `Release()` calls, same reason as above.

### `gocqlxtest/gocqlxtest.go`

- `gocql.SnappyCompressor` → `snappy.SnappyCompressor`; compressors moved into
  `github.com/apache/cassandra-gocql-driver/v2/snappy` in v2.

### `cmd/schemagen` — retargeted to the Apache metadata API

The two drivers model keyspace metadata differently:

| ScyllaDB fork | Apache v2 |
|---|---|
| `KeyspaceMetadata.Types` | `KeyspaceMetadata.UserTypes` |
| `KeyspaceMetadata.Views` | folded into `KeyspaceMetadata.Tables` |
| `KeyspaceMetadata.Indexes` | not exposed |
| `ColumnMetadata.Type` (schema string) | `ColumnMetadata.Validator` (schema string); `.Type` is a `TypeInfo` |
| `UserTypeMetadata.FieldTypes` (`[]string`) | `[]TypeInfo` |

- Materialized views still generate if a keyspace has any. `getTableMetadata` in the Apache
  driver unions `system_schema.tables` with `system_schema.views`, so views arrive as ordinary
  `TableMetadata` complete with columns, partition key and clustering columns. They now
  land in the "Table models" block rather than a separate "Materialized view models"
  block — **the generated identifiers are unchanged**, since both spellings camelize the
  same view name.

  The schemagen test no longer creates one. Cassandra 5.0 ships views disabled
  (`materialized_views_enabled: false`) and kiss2 does not use them, so creating a view made
  the suite unrunnable against a default-configured cluster. The view only ever demonstrated
  that `-ignore-names` filters views as well as tables; `composers` still covers filtering a
  table, and the golden fixture is unchanged because both names were ignored anyway.
- Index models are gone, and the `-ignore-indexes` flag was removed with them. The Apache
  driver exposes no index metadata (`system_schema.indexes` is unread).
- `type_info.go` is new. The Apache driver's `TypeInfo` implementations are unexported and
  have no `String()` method, and it keeps raw schema strings in an unexported field.
  Column types are unaffected because the raw string survives in `ColumnMetadata.Validator`,
  but UDT field types are only ever exposed as `TypeInfo`, so `typeInfoToCQL` renders them
  back to CQL from the type code before `mapScyllaToGoType` runs.

### `cmd/schemagen` — `uuid` and `timeuuid` map to `gocql.UUID`, not `[16]byte`

Upstream maps both to `[16]byte`, which the ScyllaDB driver accepted everywhere. The Apache
driver does not: its `uuidUnmarshal` accepts `*[16]byte` for a 16-byte value but omits it from
the `len(data) == 0` branch, which handles only `*UUID`, `*[]byte`, `*string` and
`*interface{}`. Scanning a **NULL** `uuid` column into a `[16]byte` field therefore fails with:

```
can not unmarshal UUID into *[16]uint8. Accepted types: *UUID, *[]byte, *string, *interface{}.
```

Measured against both databases with both drivers, on a NULL and a populated scalar `uuid`:

| Generated type | ScyllaDB driver | Apache driver, NULL | Apache driver, populated |
|---|---|---|---|
| `[16]byte` | ok | **fails** | ok |
| `gocql.UUID` | ok | ok | ok |
| `uuid.UUID` (google) | ok | **fails** | **fails** |

The results are identical against ScyllaDB and Cassandra, so this is a property of the
**driver**, not of the server — the old mapping worked because `scylladb/gocql` rewrote the
pointer and delegated to a permissive generic helper, which the Apache driver replaced with a
closed type switch. `gocql.UUID` is the only type that works in every combination, which is
why it is now what `schemagen` emits.

Only NULL **scalar** columns were ever affected. A NULL collection arrives as an empty slice
and unmarshals no elements, so `set<uuid>` → `[][16]byte` never failed; collections change to
`[]gocql.UUID` purely for consistency, because the element type comes from the same map.

The driver import is added whenever a column type *contains* `uuid`, so collections are
covered too.

**Consequence for callers.** `gocql.UUID` and `google/uuid.UUID` are both *defined* types, so
assigning between them needs an explicit conversion — the implicit
`uuid.UUID` → unnamed `[16]byte` assignment that generated models used to allow is gone.
Repository adapters must spell the conversion out in both directions:

```go
Id: gocql.UUID(entity.ID),   // ToSchema
ID: uuid.UUID(record.Id),    // ToDomain
```

Rejected alternatives: forking the Apache driver to restore the missing `case *[16]byte` (two
lines, but a second fork to maintain and permanent divergence from upstream for every service),
and retargeting `*[16]byte` scan targets to `*gocql.UUID` inside `udtWrapValue` (works, and
needs no caller changes, but hides a type substitution in the scan path and leaves generated
models readable only through this fork).

### `queryx_wrap.go`, `batchx.go` — wrappers for methods the Apache driver added

Upstream's `TestQueryxAllWrapped` / `TestBatchAllWrapped` require every embedded driver
method returning `*Query`/`*Batch` to be re-wrapped so chaining stays on the gocqlx type.
The Apache driver has methods the ScyllaDB fork does not, so wrappers were added for
`Queryx.SetKeyspace`, `Queryx.WithNowInSeconds`, `Queryx.SetHostID`, `Batch.Consistency`,
`Batch.SetKeyspace` and `Batch.WithNowInSeconds`. Both invariant tests pass.

### `queryx.go` — `GetCAS` no longer leaves stale data in `dest`

Lightweight transactions return different result sets per server:

| server | applied LWT returns | rejected LWT returns |
|---|---|---|
| Cassandra 5.0 | `[applied]=true` only | `[applied]=false` + conflicting row |
| ScyllaDB 6.2 | `[applied]=true` + pre-image | `[applied]=false` + conflicting row |

Upstream scans whatever the server sends straight into `dest` and leaves it untouched
otherwise. On Cassandra that means an applied LWT leaves the caller's **own input** sitting
in `dest`, which reads exactly like a pre-image the server never sent — a silent wrong
value, and the caller has no way to tell.

`GetCAS` now resets `dest` before scanning, so it ends up holding exactly what the server
sent and nothing else. On Cassandra an applied transaction leaves `dest` zeroed; on
ScyllaDB it holds the genuine pre-image. Branch on `applied` rather than inspecting `dest`.
The value simply does not exist on Cassandra for an applied transaction — it cannot be
recovered from the transaction, only by a separate, non-transactional read.

Observed on both servers with `dest` pre-loaded with caller input (`Salary: 999999`):

```
CASSANDRA    applied=true  dest={ID:0 Salary:0}       <- zeroed, input discarded
CASSANDRA    applied=false dest={ID:0 Salary:2000}    <- conflicting row
SCYLLA       applied=true  dest={ID:0 Salary:1000}    <- real pre-image
SCYLLA       applied=false dest={ID:0 Salary:2000}    <- conflicting row
```

`ExecCAS`, which returns only the applied flag, is unaffected. `TestIterxCAS` was updated
to assert this guarantee instead of ScyllaDB's pre-image behaviour.

## Verification

`qb` and `table` unit tests pass unmodified. The integration suites for the root package,
`migrate` and `dbutil` pass against **both** Cassandra 5.0.8 and ScyllaDB 6.2.3:

```
go test -tags integration -count=1 . ./migrate ./dbutil -cluster=127.0.0.1:9042
```

Run each package from a clean keyspace — upstream's suite does not drop `gocqlx_test`
between runs, so a stale keyspace makes `TestPending` and `TestMigration` fail on either
server.

Additionally the following was exercised against live Cassandra 5.0.8:

- connect with a token-aware host policy, exponential reconnection and retry policies
- `BindStruct` insert via `ExecRelease`
- `session.Batch(gocql.UnloggedBatch).WithContext(ctx)` + `Batch.BindStruct` + `ExecuteBatch`
- `qb.Select` with `Bind`, `Select`, `Get`, and `Iterx.StructScan`
- `gocql.ErrNotFound` on a missing row
- `gocql.UnsetValue` through a bind transformer
- reading a materialized view through a `table.Metadata` model
- `cmd/schemagen` against a keyspace with a UDT, collections, a `decimal`, and a
  materialized view

`cmd/schemagen`'s golden-file test needs a live cluster, so it is not part of a plain
`go test ./...`:

```bash
go test ./cmd/schemagen -cluster=127.0.0.1:9042      # add -update to regenerate
```

It passes against Cassandra, and the regenerated `testdata/models.go` is **byte-identical
to upstream's** — the retargeted generator reproduces upstream output exactly for that
schema, UDT and `duration` column included. `runSchemagen` was changed to honour the
`-cluster` flag; upstream hardcodes `127.0.1.1`, which only resolves on its own CI.

`testdata/no_ignore_indexes/` was removed along with the `-ignore-indexes` flag, since the
Apache driver exposes no index metadata for the generator to act on.

The golden fixture is **Cassandra-specific**, so this one test is expected to fail against
ScyllaDB. Scylla backs secondary indexes with materialized views, so `system_schema.views`
reports an extra `songs_title_index` that Cassandra does not have and the generator dutifully
emits a `SongsTitleIndex` model for it. That is schemagen reflecting a real schema
difference, not a defect. Every other suite passes on both servers.
