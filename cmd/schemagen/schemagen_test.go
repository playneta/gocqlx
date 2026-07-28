package main

import (
	"flag"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/go-cmp/cmp"

	"github.com/playneta/gocqlx/v3"
	"github.com/playneta/gocqlx/v3/gocqlxtest"
)

var flagUpdate = flag.Bool("update", false, "update golden file")

func TestSchemagen(t *testing.T) {
	flag.Parse()
	createTestSchema(t)

	// add ignored types and table
	*flagIgnoreNames = strings.Join([]string{
		"composers",
		"label",
	}, ",")

	b := runSchemagen(t, "schemagentest")
	assertDiff(t, b, "testdata/models.go")
}

func Test_usedInTables(t *testing.T) {
	tests := map[string]struct {
		columnValidator string
		typeName        string
	}{
		"matches given a frozen collection": {
			columnValidator: "frozen<album>",
			typeName:        "album",
		},
		"matches given a set": {
			columnValidator: "set<artist>",
			typeName:        "artist",
		},
		"matches given a list": {
			columnValidator: "list<song>",
			typeName:        "song",
		},
		"matches given a tuple: first of two elements": {
			columnValidator: "tuple<first, second>",
			typeName:        "first",
		},
		"matches given a tuple: second of two elements": {
			columnValidator: "tuple<first, second>",
			typeName:        "second",
		},
		"matches given a tuple: first of three elements": {
			columnValidator: "tuple<first, second, third>",
			typeName:        "first",
		},
		"matches given a tuple: second of three elements": {
			columnValidator: "tuple<first, second, third>",
			typeName:        "second",
		},
		"matches given a tuple: third of three elements": {
			columnValidator: "tuple<first, second, third>",
			typeName:        "third",
		},
		"matches given a frozen set": {
			columnValidator: "set<frozen<album>>",
			typeName:        "album",
		},
		"matches snake_case names given a nested map": {
			columnValidator: "map<album, tuple<first, map<map_key, map-value>, third>>",
			typeName:        "map_key",
		},
		"matches a nested map without spaces": {
			columnValidator: "map<album,set<song>>",
			typeName:        "song",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tables := map[string]*gocql.TableMetadata{
				"table": {Columns: map[string]*gocql.ColumnMetadata{
					"column": {Validator: tt.columnValidator},
				}},
			}
			if !usedInTables(tt.typeName, tables) {
				t.Fatal()
			}
		})
	}

	t.Run("doesn't panic with empty type name", func(t *testing.T) {
		tables := map[string]*gocql.TableMetadata{
			"table": {Columns: map[string]*gocql.ColumnMetadata{
				"column": {Validator: "map<text, album>"},
			}},
		}
		usedInTables("", tables)
	})
}

// unresolvedTypeInfo stands in for the driver's unexported unknownTypeInfo: the
// raw schema string the Apache driver keeps for a UDT field type that refers to
// another UDT, which it cannot parse.
type unresolvedTypeInfo string

func (unresolvedTypeInfo) Type() gocql.Type                           { return gocql.TypeCustom }
func (unresolvedTypeInfo) Zero() interface{}                          { return nil }
func (unresolvedTypeInfo) Marshal(interface{}) ([]byte, error)        { return nil, nil }
func (unresolvedTypeInfo) Unmarshal(data []byte, v interface{}) error { return nil }

func nativeTypeInfo(typ gocql.Type) gocql.TypeInfo {
	return gocql.NewNativeType(4, typ, "")
}

func TestRenderTemplateRetainsNestedUserTypes(t *testing.T) {
	pkgname := *flagPkgname
	ignoreNames := *flagIgnoreNames
	t.Cleanup(func() {
		*flagPkgname = pkgname
		*flagIgnoreNames = ignoreNames
	})

	*flagPkgname = "schemagentest"
	*flagIgnoreNames = ""

	text := nativeTypeInfo(gocql.TypeText)
	stateColumn := &gocql.ColumnMetadata{Name: "state", Validator: "frozen<state>"}
	b, err := renderTemplate(&gocql.KeyspaceMetadata{
		Tables: map[string]*gocql.TableMetadata{
			"states": {
				Name:           "states",
				Columns:        map[string]*gocql.ColumnMetadata{"state": stateColumn},
				OrderedColumns: []string{"state"},
				PartitionKey:   []*gocql.ColumnMetadata{stateColumn},
			},
		},
		UserTypes: map[string]*gocql.UserTypeMetadata{
			"state": {
				Name:       "state",
				FieldNames: []string{"rca", "status", "rolecustom"},
				FieldTypes: []gocql.TypeInfo{
					unresolvedTypeInfo("frozen<set<rcas>>"),
					unresolvedTypeInfo("frozen<incidentstatus>"),
					unresolvedTypeInfo("frozen<map<incidentcustomuserrole, set<userid>>>"),
				},
			},
			"rcas": {
				Name:       "rcas",
				FieldNames: []string{"id", "created", "elapsed", "score", "author"},
				FieldTypes: []gocql.TypeInfo{
					text,
					nativeTypeInfo(gocql.TypeTimestamp),
					nativeTypeInfo(gocql.TypeDuration),
					nativeTypeInfo(gocql.TypeDecimal),
					nativeTypeInfo(gocql.TypeUUID),
				},
			},
			"incidentstatus": {
				Name:       "incidentstatus",
				FieldNames: []string{"id"},
				FieldTypes: []gocql.TypeInfo{text},
			},
			"incidentcustomuserrole": {
				Name:       "incidentcustomuserrole",
				FieldNames: []string{"id"},
				FieldTypes: []gocql.TypeInfo{text},
			},
			"userid": {
				Name:       "userid",
				FieldNames: []string{"id"},
				FieldTypes: []gocql.TypeInfo{text},
			},
			"orphan": {
				Name:       "orphan",
				FieldNames: []string{"id"},
				FieldTypes: []gocql.TypeInfo{text},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	source := string(b)
	normalizedSource := strings.Join(strings.Fields(source), " ")
	for _, want := range []string{
		"type StateUserType struct {",
		"Rca []RcasUserType `cql:\"rca\"`",
		"Status IncidentstatusUserType `cql:\"status\"`",
		"Rolecustom map[IncidentcustomuserroleUserType][]UseridUserType `cql:\"rolecustom\"`",
		`"github.com/apache/cassandra-gocql-driver/v2"`,
		"type RcasUserType struct {",
		"Id string `cql:\"id\"`",
		"Created time.Time `cql:\"created\"`",
		"Elapsed gocql.Duration `cql:\"elapsed\"`",
		"Score inf.Dec `cql:\"score\"`",
		"Author gocql.UUID `cql:\"author\"`",
		"type IncidentstatusUserType struct {",
		"type IncidentcustomuserroleUserType struct {",
		"type UseridUserType struct {",
		`"gopkg.in/inf.v0"`,
		`"time"`,
	} {
		if !strings.Contains(normalizedSource, want) {
			t.Fatalf("missing generated source %q:\n%s", want, source)
		}
	}
	if strings.Contains(normalizedSource, "type OrphanUserType struct {") {
		t.Fatalf("orphan UDT should not be generated:\n%s", source)
	}
}

func TestRenderTemplateRejectsNonComparableMapKeys(t *testing.T) {
	pkgname := *flagPkgname
	ignoreNames := *flagIgnoreNames
	t.Cleanup(func() {
		*flagPkgname = pkgname
		*flagIgnoreNames = ignoreNames
	})

	*flagPkgname = "schemagentest"
	*flagIgnoreNames = ""

	tests := []struct {
		name string
		md   *gocql.KeyspaceMetadata
		want string
	}{
		{
			name: "collection key",
			md: &gocql.KeyspaceMetadata{
				Tables: map[string]*gocql.TableMetadata{
					"bad_table": {
						Name: "bad_table",
						Columns: map[string]*gocql.ColumnMetadata{
							"bad": {Name: "bad", Validator: "map<frozen<set<int>>, text>"},
						},
					},
				},
			},
			want: `table bad_table column bad: unsupported non-comparable CQL map key type "frozen<set<int>>"`,
		},
		{
			name: "UDT key with collection field",
			md: &gocql.KeyspaceMetadata{
				Tables: map[string]*gocql.TableMetadata{
					"states": {
						Name: "states",
						Columns: map[string]*gocql.ColumnMetadata{
							"state": {Name: "state", Validator: "state"},
						},
					},
				},
				UserTypes: map[string]*gocql.UserTypeMetadata{
					"state": {
						Name:       "state",
						FieldNames: []string{"roles"},
						FieldTypes: []gocql.TypeInfo{unresolvedTypeInfo("map<frozen<role>, text>")},
					},
					"role": {
						Name:       "role",
						FieldNames: []string{"ids"},
						FieldTypes: []gocql.TypeInfo{gocql.NewNativeType(4, gocql.TypeSet, "text")},
					},
				},
			},
			want: `UDT state field roles: unsupported non-comparable CQL map key type "frozen<role>"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := renderTemplate(tt.md)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestGeneratedUserTypesRoundTripNestedUDTs(t *testing.T) {
	// The Apache driver builds collection TypeInfo only from a type string, and a
	// UDT element only from its Java marshal class form.
	javaUDT := func(name string) string {
		return fmt.Sprintf("org.apache.cassandra.db.marshal.UserType(ks,%x,%x:org.apache.cassandra.db.marshal.UTF8Type)", name, "id")
	}
	stateType := gocql.UDTTypeInfo{
		Name: "state",
		Elements: []gocql.UDTField{
			{
				Name: "rca",
				Type: gocql.NewNativeType(4, gocql.TypeSet, javaUDT("rcas")),
			},
			{
				Name: "status",
				Type: gocql.UDTTypeInfo{
					Name:     "incidentstatus",
					Elements: []gocql.UDTField{{Name: "id", Type: nativeTypeInfo(gocql.TypeText)}},
				},
			},
			{
				Name: "rolecustom",
				Type: gocql.NewNativeType(4, gocql.TypeMap,
					javaUDT("incidentcustomuserrole")+", org.apache.cassandra.db.marshal.SetType("+javaUDT("userid")+")"),
			},
		},
	}

	original := generatedStateUserType{
		Rca:    []generatedRcasUserType{{Id: "rca-1"}},
		Status: generatedIncidentstatusUserType{Id: "status-1"},
		Rolecustom: map[generatedIncidentcustomuserroleUserType][]generatedUseridUserType{
			{Id: "role-1"}: {{Id: "user-1"}},
		},
	}

	data, err := gocql.Marshal(stateType, newTestGocqlxUDT(original))
	if err != nil {
		t.Fatal(err)
	}

	var decoded generatedStateUserType
	if err := gocql.Unmarshal(stateType, data, newTestGocqlxUDT(&decoded)); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(original, decoded); diff != "" {
		t.Fatal(diff)
	}
}

type testGocqlxUDT struct {
	field map[string]reflect.Value
}

func newTestGocqlxUDT(value interface{}) testGocqlxUDT {
	v := reflect.ValueOf(value)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	return testGocqlxUDT{
		field: gocqlx.DefaultMapper.FieldMap(v),
	}
}

func (u testGocqlxUDT) MarshalUDT(name string, info gocql.TypeInfo) ([]byte, error) {
	value, ok := u.field[name]
	if ok {
		return gocql.Marshal(info, value.Interface())
	}
	return nil, nil
}

func (u testGocqlxUDT) UnmarshalUDT(name string, info gocql.TypeInfo, data []byte) error {
	value, ok := u.field[name]
	if ok {
		return gocql.Unmarshal(info, data, value.Addr().Interface())
	}
	return nil
}

type generatedStateUserType struct {
	gocqlx.UDT
	Rca        []generatedRcasUserType                                               `cql:"rca"`
	Status     generatedIncidentstatusUserType                                       `cql:"status"`
	Rolecustom map[generatedIncidentcustomuserroleUserType][]generatedUseridUserType `cql:"rolecustom"`
}

type generatedRcasUserType struct {
	gocqlx.UDT
	Id string `cql:"id"`
}

type generatedIncidentstatusUserType struct {
	gocqlx.UDT
	Id string `cql:"id"`
}

type generatedIncidentcustomuserroleUserType struct {
	gocqlx.UDT
	Id string `cql:"id"`
}

type generatedUseridUserType struct {
	gocqlx.UDT
	Id string `cql:"id"`
}

func assertDiff(t *testing.T, actual []byte, goldenFile string) {
	t.Helper()

	if *flagUpdate {
		if err := os.WriteFile(goldenFile, actual, os.ModePerm); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(golden), string(actual)); diff != "" {
		t.Fatal(diff)
	}
}

func createTestSchema(t *testing.T) {
	t.Helper()

	session := gocqlxtest.CreateSession(t)
	defer session.Close()

	err := session.ExecStmt(`CREATE KEYSPACE IF NOT EXISTS schemagen WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1}`)
	if err != nil {
		t.Fatal("create keyspace:", err)
	}

	err = session.ExecStmt(`CREATE TABLE IF NOT EXISTS schemagen.songs (
		id uuid PRIMARY KEY,
		title text,
		album text,
		artist text,
		duration duration,
		tags set<text>,
		data blob)`)
	if err != nil {
		t.Fatal("create table:", err)
	}

	err = session.ExecStmt(`CREATE TYPE IF NOT EXISTS schemagen.album (
		name text,
		songwriters set<text>,)`)
	if err != nil {
		t.Fatal("create type:", err)
	}

	err = session.ExecStmt(`CREATE TABLE IF NOT EXISTS schemagen.playlists (
		id uuid,
		title text,
		album frozen<album>, 
		artist text,
		song_id uuid,
		PRIMARY KEY (id, title, album, artist))`)
	if err != nil {
		t.Fatal("create table:", err)
	}

	err = session.ExecStmt(`CREATE INDEX IF NOT EXISTS songs_title ON schemagen.songs (title)`)
	if err != nil {
		t.Fatal("create index:", err)
	}

	err = session.ExecStmt(`CREATE TABLE IF NOT EXISTS schemagen.composers (
		id uuid PRIMARY KEY,
		name text)`)
	if err != nil {
		t.Fatal("create table:", err)
	}

	// No materialized view here on purpose. Cassandra 5.0 ships them disabled
	// (materialized_views_enabled: false) and we do not use them, so creating one would make
	// this suite unrunnable against a default-configured cluster. The view only ever existed
	// to show that -ignore-names filters views as well as tables, and `composers` below still
	// covers filtering a table.

	err = session.ExecStmt(`CREATE TYPE IF NOT EXISTS schemagen.label (
		name text,
		artists set<text>)`)
	if err != nil {
		t.Fatal("create type:", err)
	}
}

func runSchemagen(t *testing.T, pkgname string) []byte {
	t.Helper()

	dir, err := os.MkdirTemp("", "gocqlx")
	if err != nil {
		t.Fatal(err)
	}
	keyspace := "schemagen"

	// Upstream hardcodes 127.0.1.1 here, which only resolves on its own CI. Reuse
	// the -cluster flag that gocqlxtest registers so the generator runs against
	// the same node as createTestSchema above.
	cl := "127.0.1.1"
	if f := flag.Lookup("cluster"); f != nil {
		cl = f.Value.String()
	}

	flagCluster = &cl
	flagKeyspace = &keyspace
	flagPkgname = &pkgname
	flagOutput = &dir

	if err := schemagen(); err != nil {
		t.Fatalf("schemagen() error %s", err)
	}

	f := fmt.Sprintf("%s/%s.go", dir, pkgname)
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("%s: %s", f, err)
	}
	return b
}
