// Copyright 2021 Molecula Corp. All rights reserved.
package planner_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	pilosa "github.com/molecula/featurebase/v3"
	"github.com/molecula/featurebase/v3/pql"
	"github.com/molecula/featurebase/v3/sql3/parser"
	planner_types "github.com/molecula/featurebase/v3/sql3/planner/types"
	sql_test "github.com/molecula/featurebase/v3/sql3/test"
	"github.com/molecula/featurebase/v3/test"
	"github.com/stretchr/testify/assert"
)

func TestPlanner_Show(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	index, err := c.GetHolder(0).CreateIndex("i", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := index.CreateField("f", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := index.CreateField("x", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	index2, err := c.GetHolder(0).CreateIndex("i2", pilosa.IndexOptions{TrackExistence: false})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := index2.CreateField("f", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := index2.CreateField("x", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	t.Run("ShowTables", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SHOW TABLES`)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 2 {
			t.Fatal(fmt.Errorf("unexpected result set length"))
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "name", Type: parser.NewDataTypeString()},
			{Name: "created_at", Type: parser.NewDataTypeTimestamp()},
			{Name: "track_existence", Type: parser.NewDataTypeBool()},
			{Name: "keys", Type: parser.NewDataTypeBool()},
			{Name: "shard_width", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("ShowColumns", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SHOW COLUMNS FROM i`)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 3 {
			t.Fatal(fmt.Errorf("unexpected result set length: %d", len(results)))
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "name", Type: parser.NewDataTypeString()},
			{Name: "type", Type: parser.NewDataTypeString()},
			{Name: "internal_type", Type: parser.NewDataTypeString()},
			{Name: "created_at", Type: parser.NewDataTypeTimestamp()},
			{Name: "keys", Type: parser.NewDataTypeBool()},
			{Name: "cache_type", Type: parser.NewDataTypeString()},
			{Name: "cache_size", Type: parser.NewDataTypeInt()},
			{Name: "scale", Type: parser.NewDataTypeInt()},
			{Name: "min", Type: parser.NewDataTypeInt()},
			{Name: "max", Type: parser.NewDataTypeInt()},
			{Name: "timeunit", Type: parser.NewDataTypeString()},
			{Name: "epoch", Type: parser.NewDataTypeInt()},
			{Name: "timequantum", Type: parser.NewDataTypeString()},
			{Name: "ttl", Type: parser.NewDataTypeString()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("ShowColumns2", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SHOW COLUMNS FROM i2`)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 3 {
			t.Fatal(fmt.Errorf("unexpected result set length"))
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "name", Type: parser.NewDataTypeString()},
			{Name: "type", Type: parser.NewDataTypeString()},
			{Name: "internal_type", Type: parser.NewDataTypeString()},
			{Name: "created_at", Type: parser.NewDataTypeTimestamp()},
			{Name: "keys", Type: parser.NewDataTypeBool()},
			{Name: "cache_type", Type: parser.NewDataTypeString()},
			{Name: "cache_size", Type: parser.NewDataTypeInt()},
			{Name: "scale", Type: parser.NewDataTypeInt()},
			{Name: "min", Type: parser.NewDataTypeInt()},
			{Name: "max", Type: parser.NewDataTypeInt()},
			{Name: "timeunit", Type: parser.NewDataTypeString()},
			{Name: "epoch", Type: parser.NewDataTypeInt()},
			{Name: "timequantum", Type: parser.NewDataTypeString()},
			{Name: "ttl", Type: parser.NewDataTypeString()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("ShowColumnsFromNotATable", func(t *testing.T) {
		_, _, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SHOW COLUMNS FROM foo`)
		if err != nil {
			if err.Error() != "[1:19] table 'foo' not found" {
				t.Fatal(err)
			}
		}
	})
}
func TestPlanner_CoverCreateTable(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	server := c.GetNode(0).Server

	t.Run("Invalid", func(t *testing.T) {
		tableName := "invalidfieldcontraints"

		fields := []struct {
			name        string
			typ         string
			constraints string
			expErr      string
		}{
			{
				name:        "stringsetcolq",
				typ:         "stringset",
				constraints: "cachetype lru size 1000 timequantum 'YMD' ttl '24h'",
				expErr:      "[1:60] 'CACHETYPE' constraint conflicts with 'TIMEQUANTUM'",
			},
			{
				name:        "stringsetcolq",
				typ:         "stringset",
				constraints: "timequantum 'YMD' ttl '24h' cachetype ranked",
				expErr:      "[1:60] 'CACHETYPE' constraint conflicts with 'TIMEQUANTUM'",
			},
		}

		for i, fld := range fields {
			if fld.name == "" {
				t.Fatalf("field name at slice index %d is blank", i)
			}
			if fld.typ == "" {
				t.Fatalf("field type at slice index %d is blank", i)
			}

			// Build the create table statement based on the fields slice above.
			sql := "create table " + tableName + "_" + fld.name + " (_id id, "
			sql += fld.name + " " + fld.typ + " " + fld.constraints
			sql += `) keypartitions 12 shardwidth 1024`

			// Run the create table statement.
			_, _, err := sql_test.MustQueryRows(t, server, sql)
			if assert.Error(t, err) {
				assert.Equal(t, fld.expErr, err.Error())
				//sql3.SQLErrConflictingColumnConstraint.Message
			}
		}
	})

	t.Run("Valid", func(t *testing.T) {
		tableName := "validfieldcontraints"

		fields := []struct {
			name        string
			typ         string
			constraints string
			expOptions  pilosa.FieldOptions
		}{
			{
				name: "_id",
				typ:  "id",
			},
			{
				name:        "intcol",
				typ:         "int",
				constraints: "min 100 max 10000",
				expOptions: pilosa.FieldOptions{
					Type: "int",
					Base: 100,
					Min:  pql.NewDecimal(100, 0),
					Max:  pql.NewDecimal(10000, 0),
				},
			},
			{
				name:        "boolcol",
				typ:         "bool",
				constraints: "",
				expOptions: pilosa.FieldOptions{
					Type: "bool",
				},
			},
			{
				name:        "timestampcol",
				typ:         "timestamp",
				constraints: "timeunit 'ms' epoch '2021-01-01T00:00:00Z'",
				expOptions: pilosa.FieldOptions{
					Base:     1609459200000,
					Type:     "timestamp",
					TimeUnit: "ms",
					Min:      pql.NewDecimal(-63745055999000, 0),
					Max:      pql.NewDecimal(251792841599000, 0),
				},
			},
			{
				name:        "decimalcol",
				typ:         "decimal(2)",
				constraints: "",
				expOptions: pilosa.FieldOptions{
					Type:  "decimal",
					Scale: 2,
					Min:   pql.NewDecimal(-9223372036854775808, 2),
					Max:   pql.NewDecimal(9223372036854775807, 2),
				},
			},
			{
				name:        "stringcol",
				typ:         "string",
				constraints: "cachetype ranked size 1000",
				expOptions: pilosa.FieldOptions{
					Type:      "mutex",
					Keys:      true,
					CacheType: "ranked",
					CacheSize: 1000,
				},
			},
			{
				name:        "stringsetcol",
				typ:         "stringset",
				constraints: "cachetype lru size 1000",
				expOptions: pilosa.FieldOptions{
					Type:      "set",
					Keys:      true,
					CacheType: "lru",
					CacheSize: 1000,
				},
			},
			{
				name:        "stringsetcolq",
				typ:         "stringset",
				constraints: "timequantum 'YMD' ttl '24h'",
				expOptions: pilosa.FieldOptions{
					Type:        "time",
					Keys:        true,
					CacheType:   "",
					CacheSize:   0,
					TimeQuantum: "YMD",
					TTL:         time.Duration(24 * time.Hour),
				},
			},
			{
				name:        "idcol",
				typ:         "id",
				constraints: "cachetype ranked size 1000",
				expOptions: pilosa.FieldOptions{
					Type:      "mutex",
					Keys:      false,
					CacheType: "ranked",
					CacheSize: 1000,
				},
			},
			{
				name:        "idsetcol",
				typ:         "idset",
				constraints: "cachetype lru",
				expOptions: pilosa.FieldOptions{
					Type:      "set",
					Keys:      false,
					CacheType: "lru",
					CacheSize: pilosa.DefaultCacheSize,
				},
			},
			{
				name:        "idsetcolsz",
				typ:         "idset",
				constraints: "cachetype lru size 1000",
				expOptions: pilosa.FieldOptions{
					Type:      "set",
					Keys:      false,
					CacheType: "lru",
					CacheSize: 1000,
				},
			},
			{
				name:        "idsetcolq",
				typ:         "idset",
				constraints: "timequantum 'YMD' ttl '24h'",
				expOptions: pilosa.FieldOptions{
					Type:        "time",
					Keys:        false,
					CacheType:   "",
					CacheSize:   0,
					TimeQuantum: "YMD",
					TTL:         time.Duration(24 * time.Hour),
				},
			},
		}

		// Build the create table statement based on the fields slice above.
		sql := "create table " + tableName + " ("
		fieldDefs := make([]string, len(fields))
		for i, fld := range fields {
			if fld.name == "" {
				t.Fatalf("field name at slice index %d is blank", i)
			}
			if fld.typ == "" {
				t.Fatalf("field type at slice index %d is blank", i)
			}
			fieldDefs[i] = fld.name + " " + fld.typ
			if fld.constraints != "" {
				fieldDefs[i] += " " + fld.constraints
			}
		}
		sql += strings.Join(fieldDefs, ", ")
		sql += `) keypartitions 12 shardwidth 65536`

		// Run the create table statement.
		results, columns, err := sql_test.MustQueryRows(t, server, sql)
		assert.NoError(t, err)
		assert.Equal(t, [][]interface{}{}, results)
		assert.Equal(t, []*planner_types.PlannerColumn{}, columns)

		// Ensure that the fields got created as expected.
		t.Run("EnsureFields", func(t *testing.T) {
			api := c.GetNode(0).API
			ctx := context.Background()

			schema, err := api.Schema(ctx, false)
			assert.NoError(t, err)
			//spew.Dump(schema)

			// Get the fields from the FeatureBase schema.
			// fbFields is a map of fieldName to FieldInfo.
			var fbFields map[string]*pilosa.FieldInfo
			var tableKeys bool
			for _, idx := range schema {
				if idx.Name == tableName {
					tableKeys = idx.Options.Keys
					fbFields = make(map[string]*pilosa.FieldInfo)
					for _, fld := range idx.Fields {
						fbFields[fld.Name] = fld
					}
				}
			}
			assert.NotNil(t, fbFields)

			// Ensure the schema field options match the expected options.
			for _, fld := range fields {
				t.Run(fmt.Sprintf("Field:%s", fld.name), func(t *testing.T) {
					// Field `_id` isn't returned from FeatureBase in the schema,
					// but we do want to validate that its type is used to determine
					// whether or not the table is keyed.
					if fld.name == "_id" {
						switch fld.typ {
						case "id":
							assert.False(t, tableKeys)
						case "string":
							assert.True(t, tableKeys)
						default:
							t.Fatalf("invalid _id type: %s", fld.typ)
						}
						return
					}

					fbField, ok := fbFields[fld.name]
					assert.True(t, ok)
					assert.Equal(t, fld.expOptions, fbField.Options)
				})
			}
		})
	})
}

func TestPlanner_CreateTable(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	server := c.GetNode(0).Server

	t.Run("CreateTableAllDataTypes", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, server, `create table allcoltypes (
			_id id,
			intcol int, 
			boolcol bool, 
			timestampcol timestamp, 
			decimalcol decimal(2), 
			stringcol string, 
			stringsetcol stringset, 
			idcol id, 
			idsetcol idset) keypartitions 12 shardwidth 65536`)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([][]interface{}{}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("CreateTableAllDataTypesAgain", func(t *testing.T) {
		_, _, err := sql_test.MustQueryRows(t, server, `create table allcoltypes (
			_id id,
			intcol int, 
			boolcol bool, 
			timestampcol timestamp, 
			decimalcol decimal(2), 
			stringcol string, 
			stringsetcol stringset, 
			idcol id, 
			idsetcol idset) keypartitions 12 shardwidth 65536`)
		if err == nil {
			t.Fatal("expected error")
		} else {
			if err.Error() != "creating index: index already exists" {
				t.Fatal(err)
			}
		}
	})

	t.Run("DropTable1", func(t *testing.T) {
		_, _, err := sql_test.MustQueryRows(t, server, `drop table allcoltypes`)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("CreateTableAllDataTypesAllConstraints", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, server, `create table allcoltypes (
			_id id,
			intcol int min 0 max 10000,
			boolcol bool,
			timestampcol timestamp timeunit 'ms' epoch '2010-01-01T00:00:00Z',
			decimalcol decimal(2),
			stringcol string cachetype ranked size 1000,
			stringsetcol stringset cachetype lru size 1000,
			stringsetcolq stringset timequantum 'YMD' ttl '24h',
			idcol id cachetype ranked size 1000,
			idsetcol idset cachetype lru,
			idsetcolsz idset cachetype lru size 1000,
			idsetcolq idset timequantum 'YMD' ttl '24h') keypartitions 12 shardwidth 65536`)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([][]interface{}{}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("ShowColumns1", func(t *testing.T) {
		_, columns, err := sql_test.MustQueryRows(t, server, `SHOW COLUMNS FROM allcoltypes`)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "name", Type: parser.NewDataTypeString()},
			{Name: "type", Type: parser.NewDataTypeString()},
			{Name: "internal_type", Type: parser.NewDataTypeString()},
			{Name: "created_at", Type: parser.NewDataTypeTimestamp()},
			{Name: "keys", Type: parser.NewDataTypeBool()},
			{Name: "cache_type", Type: parser.NewDataTypeString()},
			{Name: "cache_size", Type: parser.NewDataTypeInt()},
			{Name: "scale", Type: parser.NewDataTypeInt()},
			{Name: "min", Type: parser.NewDataTypeInt()},
			{Name: "max", Type: parser.NewDataTypeInt()},
			{Name: "timeunit", Type: parser.NewDataTypeString()},
			{Name: "epoch", Type: parser.NewDataTypeInt()},
			{Name: "timequantum", Type: parser.NewDataTypeString()},
			{Name: "ttl", Type: parser.NewDataTypeString()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("CreateTableDupeColumns", func(t *testing.T) {
		_, _, err := sql_test.MustQueryRows(t, server, `create table dupecols (
			_id id,
			_id int)`)
		if err == nil {
			t.Fatal("expected error")
		} else {
			if err.Error() != "[3:4] duplicate column '_id'" {
				t.Fatal(err)
			}
		}
	})

	t.Run("CreateTableMissingId", func(t *testing.T) {
		_, _, err := sql_test.MustQueryRows(t, server, `create table missingid (
			foo int)`)
		if err == nil {
			t.Fatal("expected error")
		} else {
			if err.Error() != "[1:1] _id column must be specified" {
				t.Fatal(err)
			}
		}
	})
}

func TestPlanner_AlterTable(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	index, err := c.GetHolder(0).CreateIndex("i", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := index.CreateField("f", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := index.CreateField("x", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	server := c.GetNode(0).Server

	t.Run("AlterTableDrop", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, server, `alter table i drop column f`)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([][]interface{}{}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("AlterTableAdd", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, server, `alter table i add column f int`)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([][]interface{}{}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("AlterTableRename", func(t *testing.T) {
		t.Skip("not yet implemented")
		results, columns, err := sql_test.MustQueryRows(t, server, `alter table i rename column f to g`)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([][]interface{}{}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

}
func TestPlanner_DropTable(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	index, err := c.GetHolder(0).CreateIndex("i", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := index.CreateField("f", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := index.CreateField("x", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	t.Run("DropTable", func(t *testing.T) {
		_, _, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `DROP TABLE i`)
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestPlanner_ExpressionsInSelectListParen(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	i0, err := c.GetHolder(0).CreateIndex("i0", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i0.CreateField("a", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := i0.CreateField("b", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	i1, err := c.GetHolder(0).CreateIndex("i1", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i1.CreateField("x", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := i1.CreateField("y", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	// Populate with data.
	if _, err := c.GetNode(0).API.Query(context.Background(), &pilosa.QueryRequest{
		Index: "i0",
		Query: `
			Set(1, a=10)
			Set(1, b=100)
			Set(2, a=20)
			Set(2, b=200)
	`}); err != nil {
		t.Fatal(err)
	}

	t.Run("ParenOne", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT (a != b) = false, _id FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{bool(false), int64(1)},
			{bool(false), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "", Type: parser.NewDataTypeBool()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("ParenTwo", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT (a != b) = (false), _id FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{bool(false), int64(1)},
			{bool(false), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "", Type: parser.NewDataTypeBool()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})
}

func TestPlanner_ExpressionsInSelectListLiterals(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	i0, err := c.GetHolder(0).CreateIndex("i0", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i0.CreateField("a", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := i0.CreateField("b", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := i0.CreateField("d", pilosa.OptFieldTypeDecimal(2)); err != nil {
		t.Fatal(err)
	} else if _, err := i0.CreateField("ts", pilosa.OptFieldTypeTimestamp(pilosa.DefaultEpoch, "s")); err != nil {
		t.Fatal(err)
	} else if _, err := i0.CreateField("str", pilosa.OptFieldTypeMutex(pilosa.CacheTypeLRU, pilosa.DefaultCacheSize), pilosa.OptFieldKeys()); err != nil {
		t.Fatal(err)
	}

	// Populate with data.
	if _, err := c.GetNode(0).API.Query(context.Background(), &pilosa.QueryRequest{
		Index: "i0",
		Query: `
			Set(1, a=10)
			Set(1, b=100)
			Set(2, a=20)
			Set(2, b=200)
			Set(1, d=10.3)
			Set(1, ts='2022-02-22T22:22:22Z')
			Set(1, str='foo')
	`}); err != nil {
		t.Fatal(err)
	}

	t.Run("LiteralsBool", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT false = true, _id FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{bool(false), int64(1)},
			{bool(false), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "", Type: parser.NewDataTypeBool()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("LiteralsInt", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT 1 + 2, _id FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(3), int64(1)},
			{int64(3), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "", Type: parser.NewDataTypeInt()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("LiteralsID", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT _id + 2, _id FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(3), int64(1)},
			{int64(4), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "", Type: parser.NewDataTypeInt()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("LiteralsDecimal", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT d + 2.0, _id FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{float64(12.3), int64(1)},
			{nil, int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "", Type: parser.NewDataTypeDecimal(2)},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("LiteralsString", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT str || ' bar', _id FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{string("foo bar"), int64(1)},
			{nil, int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "", Type: parser.NewDataTypeString()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})
}

func TestPlanner_ExpressionsInSelectListCase(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	i0, err := c.GetHolder(0).CreateIndex("i0", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i0.CreateField("a", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := i0.CreateField("b", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := i0.CreateField("d", pilosa.OptFieldTypeDecimal(2)); err != nil {
		t.Fatal(err)
	} else if _, err := i0.CreateField("ts", pilosa.OptFieldTypeTimestamp(pilosa.DefaultEpoch, "s")); err != nil {
		t.Fatal(err)
	} else if _, err := i0.CreateField("str", pilosa.OptFieldTypeMutex(pilosa.CacheTypeLRU, pilosa.DefaultCacheSize), pilosa.OptFieldKeys()); err != nil {
		t.Fatal(err)
	}

	// Populate with data.
	if _, err := c.GetNode(0).API.Query(context.Background(), &pilosa.QueryRequest{
		Index: "i0",
		Query: `
			Set(1, a=10)
			Set(1, b=100)
			Set(2, a=20)
			Set(2, b=200)
			Set(1, d=10.3)
			Set(1, ts='2022-02-22T22:22:22Z')
			Set(1, str='foo')
	`}); err != nil {
		t.Fatal(err)
	}

	t.Run("CaseWithBase", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT b, case b when 100 then 10 when 201 then 20 else 5 end, _id FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(100), int64(10), int64(1)},
			{int64(200), int64(5), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "b", Type: parser.NewDataTypeInt()},
			{Name: "", Type: parser.NewDataTypeInt()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("CaseWithNoBase", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT b, case when b = 100 then 10 when b = 201 then 20 else 5 end, _id FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(100), int64(10), int64(1)},
			{int64(200), int64(5), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "b", Type: parser.NewDataTypeInt()},
			{Name: "", Type: parser.NewDataTypeInt()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})
}

func TestPlanner_Select(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	i0, err := c.GetHolder(0).CreateIndex("i0", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i0.CreateField("a", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := i0.CreateField("b", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	i1, err := c.GetHolder(0).CreateIndex("i1", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i1.CreateField("x", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := i1.CreateField("y", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	// Populate with data.
	if _, err := c.GetNode(0).API.Query(context.Background(), &pilosa.QueryRequest{
		Index: "i0",
		Query: `
			Set(1, a=10)
			Set(1, b=100)
			Set(2, a=20)
			Set(2, b=200)
	`}); err != nil {
		t.Fatal(err)
	}

	t.Run("UnqualifiedColumns", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT a, b, _id FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(10), int64(100), int64(1)},
			{int64(20), int64(200), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "a", Type: parser.NewDataTypeInt()},
			{Name: "b", Type: parser.NewDataTypeInt()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("QualifiedTableRef", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT bar.a, bar.b, bar._id FROM i0 as bar`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(10), int64(100), int64(1)},
			{int64(20), int64(200), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "a", Type: parser.NewDataTypeInt()},
			{Name: "b", Type: parser.NewDataTypeInt()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("AliasedUnqualifiedColumns", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT a as foo, b as bar, _id as baz FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(10), int64(100), int64(1)},
			{int64(20), int64(200), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "foo", Type: parser.NewDataTypeInt()},
			{Name: "bar", Type: parser.NewDataTypeInt()},
			{Name: "baz", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("QualifiedColumns", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT i0._id, i0.a, i0.b FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(1), int64(10), int64(100)},
			{int64(2), int64(20), int64(200)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "_id", Type: parser.NewDataTypeID()},
			{Name: "a", Type: parser.NewDataTypeInt()},
			{Name: "b", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("UnqualifiedStar", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT * FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(1), int64(10), int64(100)},
			{int64(2), int64(20), int64(200)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "_id", Type: parser.NewDataTypeID()},
			{Name: "a", Type: parser.NewDataTypeInt()},
			{Name: "b", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("QualifiedStar", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT i0.* FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(1), int64(10), int64(100)},
			{int64(2), int64(20), int64(200)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "_id", Type: parser.NewDataTypeID()},
			{Name: "a", Type: parser.NewDataTypeInt()},
			{Name: "b", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("NoIdentifier", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT a, b FROM i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(10), int64(100)},
			{int64(20), int64(200)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "a", Type: parser.NewDataTypeInt()},
			{Name: "b", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("ErrFieldNotFound", func(t *testing.T) {
		_, err := c.GetNode(0).Server.CompileExecutionPlan(context.Background(), `SELECT xyz FROM i0`)
		if err == nil || !strings.Contains(err.Error(), `column 'xyz' not found`) {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestPlanner_SelectOrderBy(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	i0, err := c.GetHolder(0).CreateIndex("i0", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i0.CreateField("a", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := i0.CreateField("b", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	// Populate with data.
	if _, err := c.GetNode(0).API.Query(context.Background(), &pilosa.QueryRequest{
		Index: "i0",
		Query: `
			Set(1, a=10)
			Set(1, b=100)
			Set(2, a=20)
			Set(2, b=200)
	`}); err != nil {
		t.Fatal(err)
	}

	t.Run("OrderBy", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT a, b, _id FROM i0 order by a desc`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(20), int64(200), int64(2)},
			{int64(10), int64(100), int64(1)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "a", Type: parser.NewDataTypeInt()},
			{Name: "b", Type: parser.NewDataTypeInt()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})
}

func TestPlanner_SelectSelectSource(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	i0, err := c.GetHolder(0).CreateIndex("i0", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i0.CreateField("a", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := i0.CreateField("b", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	// Populate with data.
	if _, err := c.GetNode(0).API.Query(context.Background(), &pilosa.QueryRequest{
		Index: "i0",
		Query: `
			Set(1, a=10)
			Set(1, b=100)
			Set(2, a=20)
			Set(2, b=200)
	`}); err != nil {
		t.Fatal(err)
	}

	t.Run("ParenSource", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT a, b, _id FROM (select * from i0)`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(10), int64(100), int64(1)},
			{int64(20), int64(200), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "a", Type: parser.NewDataTypeInt()},
			{Name: "b", Type: parser.NewDataTypeInt()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("ParenSourceWithAlias", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT foo.a, b, _id FROM (select * from i0) as foo`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(10), int64(100), int64(1)},
			{int64(20), int64(200), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "a", Type: parser.NewDataTypeInt()},
			{Name: "b", Type: parser.NewDataTypeInt()},
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})
}

func TestPlanner_In(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	i0, err := c.GetHolder(0).CreateIndex("i0", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i0.CreateField("a", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	i1, err := c.GetHolder(0).CreateIndex("i1", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i1.CreateField("parentid", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := i1.CreateField("x", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	// Populate with data.
	if _, err := c.GetNode(0).API.Query(context.Background(), &pilosa.QueryRequest{
		Index: "i0",
		Query: `
			Set(1, a=10)
			Set(2, a=20)
			Set(3, a=30)
	`}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.GetNode(0).API.Query(context.Background(), &pilosa.QueryRequest{
		Index: "i1",
		Query: `
			Set(1, parentid=1)
			Set(1, x=100)

			Set(2, parentid=1)
			Set(2, x=200)

			Set(3, parentid=2)
			Set(3, x=300)
	`}); err != nil {
		t.Fatal(err)
	}

	t.Run("Count", func(t *testing.T) {
		t.Skip("Need to add join conditions to get this to pass")
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT i0._id, i0.a, i1._id, i1.parentid, i1.x FROM i0 INNER JOIN i1 ON i0._id = i1.parentid`)
		//results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT COUNT(*) FROM i0 INNER JOIN i1 ON i0._id = i1.parentid`)
		//results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT a FROM i0 where a = 20`) //   SELECT COUNT(*) FROM i0 INNER JOIN i1 ON i0._id = i1.parentid
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "count", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	/*t.Run("Count", func(t *testing.T) {
		//results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT COUNT(*) FROM i0 INNER JOIN i1 ON i0._id = i1.parentid`)
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT COUNT(*) FROM i0 where i0._id in (select distinct parentid from i1)`) //   SELECT COUNT(*) FROM i0 INNER JOIN i1 ON i0._id = i1.parentid
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "count", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("CountWithParentCondition", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT COUNT(*) FROM i0 where i0._id in (select distinct parentid from i1) and i0.a = 10`) // SELECT COUNT(*) FROM i0 INNER JOIN i1 ON i0._id = i1.parentid WHERE i0.a = 10
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(1)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "count", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("CountWithParentAndChildCondition", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT COUNT(*) FROM i0 where i0._id in (select distinct parentid from i1 where x = 200) and i0.a = 10`) // SELECT COUNT(*) FROM i0 INNER JOIN i1 ON i0._id = i1.parentid WHERE i0.a = 10
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(1)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "count", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})*/
}

func TestPlanner_Distinct(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	i0, err := c.GetHolder(0).CreateIndex("i0", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i0.CreateField("a", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	i1, err := c.GetHolder(0).CreateIndex("i1", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i1.CreateField("parentid", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	} else if _, err := i1.CreateField("x", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	// Populate with data.
	if _, err := c.GetNode(0).API.Query(context.Background(), &pilosa.QueryRequest{
		Index: "i0",
		Query: `
			Set(1, a=10)
			Set(2, a=20)
			Set(3, a=30)
	`}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.GetNode(0).API.Query(context.Background(), &pilosa.QueryRequest{
		Index: "i1",
		Query: `
			Set(1, parentid=1)
			Set(1, x=100)

			Set(2, parentid=1)
			Set(2, x=200)

			Set(3, parentid=2)
			Set(3, x=300)
	`}); err != nil {
		t.Fatal(err)
	}

	t.Run("SelectDistinct_id", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT distinct _id from i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(1)},
			{int64(2)},
			{int64(3)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "_id", Type: parser.NewDataTypeID()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("SelectDistinctNonId", func(t *testing.T) {
		t.Skip("WIP")
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `SELECT distinct parentid from i1`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(1)},
			{int64(2)},
			{int64(3)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "parentid", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("SelectDistinctMultiple", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `select distinct _id, parentid from i1`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(1), int64(1)},
			{int64(2), int64(1)},
			{int64(3), int64(2)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "_id", Type: parser.NewDataTypeID()},
			{Name: "parentid", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})
}

func TestPlanner_SelectTop(t *testing.T) {
	c := test.MustRunCluster(t, 1)
	defer c.Close()

	i0, err := c.GetHolder(0).CreateIndex("i0", pilosa.IndexOptions{TrackExistence: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := i0.CreateField("a", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	if _, err := i0.CreateField("b", pilosa.OptFieldTypeInt(0, 1000)); err != nil {
		t.Fatal(err)
	}

	// Populate with data.
	if _, err := c.GetNode(0).API.Query(context.Background(), &pilosa.QueryRequest{
		Index: "i0",
		Query: `
			Set(1, a=10)
			Set(2, a=20)
			Set(3, a=30)
			Set(1, b=100)
			Set(2, b=200)
			Set(3, b=300)
	`}); err != nil {
		t.Fatal(err)
	}

	t.Run("SelectTopStar", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `select top(1) * from i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(1), int64(10), int64(100)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "_id", Type: parser.NewDataTypeID()},
			{Name: "a", Type: parser.NewDataTypeInt()},
			{Name: "b", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("SelectTopNStar", func(t *testing.T) {
		results, columns, err := sql_test.MustQueryRows(t, c.GetNode(0).Server, `select topn(1) * from i0`)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff([][]interface{}{
			{int64(1), int64(10), int64(100)},
			{int64(2), int64(20), int64(200)},
			{int64(3), int64(30), int64(300)},
		}, results); diff != "" {
			t.Fatal(diff)
		}

		if diff := cmp.Diff([]*planner_types.PlannerColumn{
			{Name: "_id", Type: parser.NewDataTypeID()},
			{Name: "a", Type: parser.NewDataTypeInt()},
			{Name: "b", Type: parser.NewDataTypeInt()},
		}, columns); diff != "" {
			t.Fatal(diff)
		}
	})
}