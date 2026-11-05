package main

import (
	"fmt"
	"reflect"
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
// can be fed to mapScyllaToGoType, which works on schema strings. It fails on a
// type code it cannot render, such as a vector, instead of emitting a bogus Go type.
func typeInfoToCQL(t gocql.TypeInfo) (string, error) {
	switch v := t.(type) {
	case gocql.CollectionType:
		switch v.Type() {
		case gocql.TypeMap:
			key, err := typeInfoToCQL(v.Key)
			if err != nil {
				return "", err
			}
			elem, err := typeInfoToCQL(v.Elem)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("map<%s, %s>", key, elem), nil
		case gocql.TypeList, gocql.TypeSet:
			elem, err := typeInfoToCQL(v.Elem)
			if err != nil {
				return "", err
			}
			name := "list"
			if v.Type() == gocql.TypeSet {
				name = "set"
			}
			return fmt.Sprintf("%s<%s>", name, elem), nil
		}
	case gocql.UDTTypeInfo:
		return v.Name, nil
	case gocql.TupleTypeInfo:
		elems := make([]string, 0, len(v.Elems))
		for _, e := range v.Elems {
			elem, err := typeInfoToCQL(e)
			if err != nil {
				return "", err
			}
			elems = append(elems, elem)
		}
		return "tuple<" + strings.Join(elems, ", ") + ">", nil
	}

	if name, ok := cqlTypeNames[t.Type()]; ok {
		return name, nil
	}

	// The driver cannot resolve a UDT referenced from a UDT field, such as
	// frozen<other_udt> or set<frozen<other_udt>>, and keeps the raw schema
	// string in an unexported string-kinded TypeInfo of type TypeCustom.
	if t.Type() == gocql.TypeCustom {
		if v := reflect.ValueOf(t); v.Kind() == reflect.String {
			return v.String(), nil
		}
	}

	return "", fmt.Errorf("typeInfoToCQL: unsupported CQL type code %d", t.Type())
}
