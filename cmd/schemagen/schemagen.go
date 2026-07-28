package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"go/format"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/apache/cassandra-gocql-driver/v2"

	"github.com/playneta/gocqlx/v3"
	_ "github.com/playneta/gocqlx/v3/table"
)

var defaultClusterConfig = gocql.NewCluster()

var (
	defaultQueryTimeout      = defaultClusterConfig.Timeout
	defaultConnectionTimeout = defaultClusterConfig.ConnectTimeout
)

var (
	cmd                           = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flagCluster                   = cmd.String("cluster", "127.0.0.1", "a comma-separated list of host:port tuples")
	flagKeyspace                  = cmd.String("keyspace", "", "keyspace to inspect")
	flagPkgname                   = cmd.String("pkgname", "models", "the name you wish to assign to your generated package")
	flagOutput                    = cmd.String("output", "models", "the name of the folder to output to")
	flagOutputDirPerm             = cmd.Uint64("output-dir-perm", 0o755, "output directory permissions")
	flagOutputFilePerm            = cmd.Uint64("output-file-perm", 0o644, "output file permissions")
	flagUser                      = cmd.String("user", "", "user for password authentication")
	flagPassword                  = cmd.String("password", "", "password for password authentication")
	flagIgnoreNames               = cmd.String("ignore-names", "", "a comma-separated list of table names to ignore")
	flagQueryTimeout              = cmd.Duration("query-timeout", defaultQueryTimeout, "query timeout ( in seconds )")
	flagConnectionTimeout         = cmd.Duration("connection-timeout", defaultConnectionTimeout, "connection timeout ( in seconds )")
	flagSSLEnableHostVerification = cmd.Bool("ssl-enable-host-verification", false, "don't check server ssl certificate")
	flagSSLCAPath                 = cmd.String("ssl-ca-path", "", "path to ssl CA certificates")
	flagSSLCertPath               = cmd.String("ssl-cert-path", "", "path to ssl certificate")
	flagSSLKeyPath                = cmd.String("ssl-key-path", "", "path to ssl key")
)

//go:embed keyspace.tmpl
var keyspaceTmpl string

func main() {
	err := cmd.Parse(os.Args[1:])
	if err != nil {
		log.Fatalln("can't parse flags")
	}

	if *flagKeyspace == "" {
		log.Fatalln("missing required flag: keyspace")
	}

	if err := schemagen(); err != nil {
		log.Fatalf("failed to generate schema: %s", err)
	}
}

func schemagen() error {
	if err := os.MkdirAll(*flagOutput, os.FileMode(*flagOutputDirPerm)); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	session, err := createSession()
	if err != nil {
		return fmt.Errorf("open output file: %w", err)
	}
	metadata, err := session.KeyspaceMetadata(*flagKeyspace)
	if err != nil {
		return fmt.Errorf("fetch keyspace metadata: %w", err)
	}
	b, err := renderTemplate(metadata)
	if err != nil {
		return fmt.Errorf("render template: %w", err)
	}
	outputPath := path.Join(*flagOutput, *flagPkgname+".go")

	return os.WriteFile(outputPath, b, fs.FileMode(*flagOutputFilePerm))
}

func renderTemplate(md *gocql.KeyspaceMetadata) ([]byte, error) {
	t, err := template.
		New("keyspace.tmpl").
		Funcs(template.FuncMap{"camelize": camelize}).
		Funcs(template.FuncMap{"mapScyllaToGoType": mapScyllaToGoType}).
		Funcs(template.FuncMap{"typeInfoToCQL": typeInfoToCQL}).
		Parse(keyspaceTmpl)
	if err != nil {
		log.Fatalln("unable to parse models template:", err)
	}

	// Remove all tables whose names match the filter.
	ignoredNames := make(map[string]struct{})
	for _, ignoredName := range strings.Split(*flagIgnoreNames, ",") {
		ignoredNames[ignoredName] = struct{}{}
	}
	for name := range ignoredNames {
		delete(md.Tables, name)
	}

	userTypeFields := userTypeFieldTypes(md.UserTypes)
	removeUnusedUserTypes(md, userTypeFields)
	if err := validateMapKeyTypes(md, userTypeFields); err != nil {
		return nil, fmt.Errorf("validate map key types: %w", err)
	}

	imports := make([]string, 0)
	if len(md.UserTypes) != 0 {
		imports = append(imports, "github.com/playneta/gocqlx/v3")
	}

	updateImport := func(scyllaType string) {
		for _, typeName := range scyllaTypeNames(scyllaType) {
			if (typeName == "timestamp" || typeName == "date" || typeName == "time") && !existsInSlice(imports, "time") {
				imports = append(imports, "time")
			}
			if typeName == "decimal" && !existsInSlice(imports, "gopkg.in/inf.v0") {
				imports = append(imports, "gopkg.in/inf.v0")
			}
			if (typeName == "duration" || typeName == "uuid" || typeName == "timeuuid") &&
				!existsInSlice(imports, "github.com/apache/cassandra-gocql-driver/v2") {
				imports = append(imports, "github.com/apache/cassandra-gocql-driver/v2")
			}
		}
	}

	updateImports := func(columns map[string]*gocql.ColumnMetadata) {
		for _, c := range columns {
			updateImport(c.Validator)
		}
	}

	// Ensure that for each table
	//
	// 1. ordered columns are sorted alphabetically;
	// 2. imports are resolved for column types.
	//
	// md.Tables also carries materialized views: getTableMetadata unions
	// system_schema.tables with system_schema.views, so views arrive as ordinary
	// TableMetadata with their own columns and keys.
	for _, t := range md.Tables {
		sort.Strings(t.OrderedColumns)
		updateImports(t.Columns)
	}
	for userTypeName := range md.UserTypes {
		for _, fieldType := range userTypeFields[userTypeName] {
			updateImport(fieldType)
		}
	}

	buf := &bytes.Buffer{}
	data := map[string]interface{}{
		"PackageName": *flagPkgname,
		"Tables":      md.Tables,
		"UserTypes":   md.UserTypes,
		"Imports":     imports,
	}

	if err = t.Execute(buf, data); err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}
	return format.Source(buf.Bytes())
}

func createSession() (gocqlx.Session, error) {
	cluster := gocql.NewCluster(clusterHosts()...)

	if *flagUser != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{
			Username: *flagUser,
			Password: *flagPassword,
		}
	}

	if *flagQueryTimeout >= 0 {
		cluster.Timeout = *flagQueryTimeout
	}
	if *flagConnectionTimeout >= 0 {
		cluster.ConnectTimeout = *flagConnectionTimeout
	}

	if *flagSSLCAPath != "" || *flagSSLCertPath != "" || *flagSSLKeyPath != "" {
		cluster.SslOpts = &gocql.SslOptions{
			EnableHostVerification: *flagSSLEnableHostVerification,
			CaPath:                 *flagSSLCAPath,
			CertPath:               *flagSSLCertPath,
			KeyPath:                *flagSSLKeyPath,
		}
	}

	return gocqlx.WrapSession(cluster.CreateSession())
}

func clusterHosts() []string {
	return strings.Split(*flagCluster, ",")
}

func existsInSlice(s []string, v string) bool {
	for _, i := range s {
		if v == i {
			return true
		}
	}

	return false
}

// userTypeFieldTypes renders every UDT field type to CQL notation, keyed by
// UDT name. The Apache driver exposes UDT field types only as TypeInfo, while
// the type parser works on CQL strings, as column validators carry them.
func userTypeFieldTypes(userTypes map[string]*gocql.UserTypeMetadata) map[string][]string {
	fieldTypes := make(map[string][]string, len(userTypes))
	for name, userType := range userTypes {
		cqlTypes := make([]string, 0, len(userType.FieldTypes))
		for _, fieldType := range userType.FieldTypes {
			cqlTypes = append(cqlTypes, typeInfoToCQL(fieldType))
		}
		fieldTypes[name] = cqlTypes
	}
	return fieldTypes
}

// removeUnusedUserTypes keeps the UDTs a table column references, directly or
// through the fields of another kept UDT, and deletes the rest.
func removeUnusedUserTypes(md *gocql.KeyspaceMetadata, userTypeFields map[string][]string) {
	usedTypes := make(map[string]struct{})
	for userTypeName := range md.UserTypes {
		if usedInTables(userTypeName, md.Tables) {
			addUsedUserType(userTypeName, userTypeFields, usedTypes)
		}
	}

	for typeName := range md.UserTypes {
		if _, ok := usedTypes[typeName]; !ok {
			delete(md.UserTypes, typeName)
		}
	}
}

func addUsedUserType(typeName string, userTypeFields map[string][]string, usedTypes map[string]struct{}) {
	if _, ok := usedTypes[typeName]; ok {
		return
	}

	fieldTypes, ok := userTypeFields[typeName]
	if !ok {
		return
	}

	usedTypes[typeName] = struct{}{}
	for _, fieldType := range fieldTypes {
		for _, fieldTypeName := range scyllaTypeNames(fieldType) {
			addUsedUserType(fieldTypeName, userTypeFields, usedTypes)
		}
	}
}

func validateMapKeyTypes(md *gocql.KeyspaceMetadata, userTypeFields map[string][]string) error {
	for tableName, table := range md.Tables {
		for columnName, column := range table.Columns {
			if err := validateScyllaMapKeys(column.Validator, userTypeFields); err != nil {
				return fmt.Errorf("table %s column %s: %w", tableName, columnName, err)
			}
		}
	}
	for typeName, userType := range md.UserTypes {
		for i, fieldType := range userTypeFields[typeName] {
			if err := validateScyllaMapKeys(fieldType, userTypeFields); err != nil {
				fieldName := fmt.Sprintf("%d", i)
				if i < len(userType.FieldNames) {
					fieldName = userType.FieldNames[i]
				}
				return fmt.Errorf("UDT %s field %s: %w", typeName, fieldName, err)
			}
		}
	}

	return nil
}

func validateScyllaMapKeys(s string, userTypes map[string][]string) error {
	name, args, ok := splitTypeConstructor(strings.TrimSpace(s))
	if !ok {
		return nil
	}

	if name == "map" && len(args) == 2 && !isComparableScyllaType(args[0], userTypes, make(map[string]struct{})) {
		return fmt.Errorf("unsupported non-comparable CQL map key type %q", args[0])
	}

	for _, arg := range args {
		if err := validateScyllaMapKeys(arg, userTypes); err != nil {
			return err
		}
	}

	return nil
}

func isComparableScyllaType(s string, userTypes map[string][]string, seen map[string]struct{}) bool {
	s = strings.TrimSpace(s)

	if goType, ok := types[s]; ok {
		return isComparableGoType(goType)
	}

	name, args, ok := splitTypeConstructor(s)
	if ok {
		switch name {
		case "frozen":
			return len(args) == 1 && isComparableScyllaType(args[0], userTypes, seen)
		case "tuple":
			for _, arg := range args {
				if !isComparableScyllaType(arg, userTypes, seen) {
					return false
				}
			}
			return true
		case "map", "set", "list":
			return false
		default:
			return true
		}
	}

	fieldTypes, ok := userTypes[s]
	if !ok {
		return true
	}
	if _, ok := seen[s]; ok {
		return true
	}

	seen[s] = struct{}{}
	defer delete(seen, s)

	for _, fieldType := range fieldTypes {
		if !isComparableScyllaType(fieldType, userTypes, seen) {
			return false
		}
	}

	return true
}

func scyllaTypeNames(s string) []string {
	s = strings.TrimSpace(s)

	name, args, ok := splitTypeConstructor(s)
	if !ok {
		return []string{s}
	}

	switch name {
	case "frozen", "map", "set", "list", "tuple":
		typeNames := make([]string, 0, len(args))
		for _, arg := range args {
			typeNames = append(typeNames, scyllaTypeNames(arg)...)
		}
		return typeNames
	default:
		return []string{s}
	}
}

func referencesScyllaType(typeName, scyllaType string) bool {
	for _, scyllaTypeName := range scyllaTypeNames(scyllaType) {
		if scyllaTypeName == typeName {
			return true
		}
	}

	return false
}

// usedInColumns tests whether the typeName is used in any of columns of the
// provided tables.
func usedInColumns(typeName string, columns map[string]*gocql.ColumnMetadata) bool {
	for _, column := range columns {
		if referencesScyllaType(typeName, column.Validator) {
			return true
		}
	}
	return false
}

// usedInTables tests whether the typeName is used in any of columns of the
// provided tables.
func usedInTables(typeName string, tables map[string]*gocql.TableMetadata) bool {
	for _, table := range tables {
		if usedInColumns(typeName, table.Columns) {
			return true
		}
	}
	return false
}
