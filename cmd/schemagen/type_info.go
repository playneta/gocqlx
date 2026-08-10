package main

import (
	"fmt"
	"strings"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// cqlTypeNames maps the CQL type codes to their schema names.
//
// The Apache driver models column types as a gocql.TypeInfo interface whose
// implementations are unexported and carry no readable String method, and it
// keeps the raw schema strings in an unexported field. Column metadata is
// unaffected because the raw string survives in ColumnMetadata.Validator, but
// UDT field types are only ever exposed as TypeInfo, so they have to be
// rendered back to CQL from the type code.
var cqlTypeNames = map[gocql.Type]string{
	gocql.TypeAscii:     "ascii",
	gocql.TypeBigInt:    "bigint",
	gocql.TypeBlob:      "blob",
	gocql.TypeBoolean:   "boolean",
	gocql.TypeCounter:   "counter",
	gocql.TypeDate:      "date",
	gocql.TypeDecimal:   "decimal",
	gocql.TypeDouble:    "double",
	gocql.TypeDuration:  "duration",
	gocql.TypeFloat:     "float",
	gocql.TypeInet:      "inet",
	gocql.TypeInt:       "int",
	gocql.TypeSmallInt:  "smallint",
	gocql.TypeText:      "text",
	gocql.TypeTime:      "time",
	gocql.TypeTimestamp: "timestamp",
	gocql.TypeTimeUUID:  "timeuuid",
	gocql.TypeTinyInt:   "tinyint",
	gocql.TypeUUID:      "uuid",
	gocql.TypeVarchar:   "varchar",
	gocql.TypeVarint:    "varint",
}

// typeInfoToCQL renders a TypeInfo back to its CQL schema notation so that it
// can be fed to mapScyllaToGoType, which works on schema strings.
func typeInfoToCQL(t gocql.TypeInfo) string {
	switch v := t.(type) {
	case gocql.CollectionType:
		switch v.Type() {
		case gocql.TypeMap:
			return fmt.Sprintf("map<%s, %s>", typeInfoToCQL(v.Key), typeInfoToCQL(v.Elem))
		case gocql.TypeList:
			return fmt.Sprintf("list<%s>", typeInfoToCQL(v.Elem))
		case gocql.TypeSet:
			return fmt.Sprintf("set<%s>", typeInfoToCQL(v.Elem))
		}
	case gocql.UDTTypeInfo:
		return v.Name
	case gocql.TupleTypeInfo:
		elems := make([]string, 0, len(v.Elems))
		for _, e := range v.Elems {
			elems = append(elems, typeInfoToCQL(e))
		}
		return "tuple<" + strings.Join(elems, ", ") + ">"
	}

	if name, ok := cqlTypeNames[t.Type()]; ok {
		return name
	}

	// A type the driver knows but this table does not is a generation bug, not a
	// runtime condition — fail loudly rather than emit a bogus Go type.
	panic(fmt.Sprintf("schemagen: unsupported CQL type code %d", t.Type()))
}
