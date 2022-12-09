// Copyright 2021 Molecula Corp. All rights reserved.
package pilosa

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/apache/arrow/go/v10/arrow"
	"github.com/apache/arrow/go/v10/arrow/array"
	"github.com/apache/arrow/go/v10/arrow/memory"
	"github.com/apache/arrow/go/v10/parquet/file"
	"github.com/apache/arrow/go/v10/parquet/pqarrow"
	"github.com/gomem/gomem/pkg/dataframe"
	"github.com/featurebasedb/featurebase/v3/pql"
	"github.com/featurebasedb/featurebase/v3/tracing"
	"github.com/featurebasedb/featurebase/v3/vprint"
	"github.com/pkg/errors"

	ivy "robpike.io/ivy/arrow"
	config "robpike.io/ivy/config"
	"robpike.io/ivy/exec"
	"robpike.io/ivy/parse"
	"robpike.io/ivy/run"
	"robpike.io/ivy/scan"
	"robpike.io/ivy/value"
)

type (
	ApplyResult *arrow.Column
)

func runIvyString(context value.Context, str string) (ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = r.(value.Error)
		}
	}()
	scanner := scan.New(context, "<args>", strings.NewReader(str))
	parser := parse.NewParser("<args>", scanner, context)
	ok = run.Run(parser, context, false)
	return
}

// Possibly combine all arrays together then apply some interesting
// computation at the end?
func IvyReduce(reduceCode string, opCode string, opt *ExecOptions) (func(ctx context.Context, prev, v interface{}) interface{}, func() (*dataframe.DataFrame, error)) {
	var accumulator value.Value
	mu := &sync.Mutex{}
	concat := value.BinaryOps[opCode]
	conf := getDefaultConfig()
	ctxIvy := exec.NewContext(&conf)
	// concat returned results at coordinating node.
	reduceFn := func(ctx context.Context, prev, v interface{}) interface{} {
		if v == nil {
			return prev
		}
		if accumulator == nil {
			switch val := v.(type) {
			case *dataframe.DataFrame:
				col := val.ColumnAt(0)
				resolver := dataframe.NewChunkResolver(col)
				accumulator = value.NewArrowVector(col, &conf, &resolver)
			case value.Value:
				accumulator = v.(value.Value)
			default:
				return errors.New(fmt.Sprintf("ivy reduction failed first unexpected type %T", v))
			}
			return nil
		}
		switch val := v.(type) {
		case *dataframe.DataFrame:
			col := val.ColumnAt(0)
			resolver := dataframe.NewChunkResolver(col)
			x := value.NewArrowVector(col, &conf, &resolver)
			mu.Lock() // i'm being overyerly cautious..need to confirm this can be concurrent
			accumulator = concat.EvalBinary(ctxIvy, accumulator, x)
			mu.Unlock()
		case value.Value:
			mu.Lock()
			accumulator = concat.EvalBinary(ctxIvy, accumulator, val)
			mu.Unlock()
		default:
			return errors.New(fmt.Sprintf("ivy reduction failed unexpected type %T", v))
		}

		return nil
	}
	tablerFn := func() (*dataframe.DataFrame, error) {
		pool := memory.NewGoAllocator() // TODO(twg) 2022/09/01 singledton?
		if opt.Remote {
			col := value.ToArrowColumn(accumulator, pool)
			return dataframe.NewDataFrameFromColumns(pool, []arrow.Column{*col})
		}
		// only acutally reduce on the initiating node i hate the network
		// over head but oh well
		ctxIvy.AssignGlobal("_", accumulator)
		ok, err := runIvyString(ctxIvy, reduceCode)
		if err != nil {
			return nil, err
		}
		if ok {
			v := ctxIvy.Global("_")
			if v == nil {
				return nil, errors.New("ivy reduction no result ")
			}
			col := value.ToArrowColumn(ctxIvy.Global("_"), pool)

			return dataframe.NewDataFrameFromColumns(pool, []arrow.Column{*col})
		}
		return nil, errors.New("ivy reduction failed ")
	}

	return reduceFn, tablerFn
}

// executeApply executes a Apply() call.
func (e *executor) executeApply(ctx context.Context, qcx *Qcx, index string, c *pql.Call, shards []uint64, opt *ExecOptions) (*dataframe.DataFrame, error) {
	if !e.dataframeEnabled {
		return nil, errors.New("Dataframe support not enabled")
	}
	span, ctx := tracing.StartSpanFromContext(ctx, "Executor.executeMax")
	defer span.Finish()

	if _, err := c.FirstStringArg("_ivy"); err != nil {
		return nil, errors.Wrap(err, " no ivy program supplied")
	}

	if len(c.Children) > 1 {
		return nil, errors.New("Apply() only accepts a single bitmap input filter")
	}

	// Execute calls in bulk on each remote node and merge.
	mapFn := func(ctx context.Context, shard uint64, mopt *mapOptions) (_ interface{}, err error) {
		return e.executeApplyShard(ctx, qcx, index, c, shard)
	}
	ivyReduce, ok, err := c.StringArg("_ivyReduce")
	if err != nil {
		return nil, err
	}
	reduceFn, tablerFn := IvyReduce("_", ",", opt)
	if ok {
		reduceFn, tablerFn = IvyReduce(ivyReduce, ",", opt)
	}

	_, err = e.mapReduce(ctx, index, shards, c, opt, mapFn, reduceFn)
	if err != nil {
		return nil, err
	}
	return tablerFn()
}

func getDefaultConfig() config.Config {
	maxbits := uint(1e9)   // "maximum size of an integer, in bits; 0 means no limit")
	maxdigits := uint(1e4) // "above this many `digits`, integers print as floating point; 0 disables")
	maxstack := uint(100000)
	origin := 1  // "set index origin to `n` (must be 0 or 1)")
	prompt := "" // flag.String("prompt", "", "command `prompt`")
	format := ""
	//      debugFlag := "" // flag.String("debug", "", "comma-separated `names` of debug settings to enable")
	conf := config.Config{}
	conf.SetFormat(format)
	conf.SetMaxBits(maxbits)
	conf.SetMaxDigits(maxdigits)
	conf.SetMaxStack(maxstack)
	conf.SetOrigin(origin)
	conf.SetPrompt(prompt)
	conf.SetOutput(io.Discard)
	conf.SetErrOutput(io.Discard)
	conf.SetEmbedded(true) // needed to propagate panic
	return conf
}

func filterDataframe(resolver dataframe.Resolver, pool memory.Allocator, filter []int64) (*dataframe.IndexResolver, error) {
	if resolver.NumRows() == 0 {
		return nil, errors.New("No data")
	}
	indexResolver := dataframe.NewIndexResolver(len(filter), uint32(ShardWidth-1))
	for i, id := range filter {
		if int(id) >= resolver.NumRows() {
			continue
		}
		c, o := resolver.Resolve(int(id))
		indexResolver.Set(i, c, o)

	}
	return indexResolver, nil
}

func (e *executor) executeApplyShard(ctx context.Context, qcx *Qcx, index string, c *pql.Call, shard uint64) (value.Value, error) {
	span, _ := tracing.StartSpanFromContext(ctx, "Executor.executeApplyShard")
	defer span.Finish()

	ivyProgram, ok, err := c.StringArg("_ivy")
	if err != nil || !ok {
		return nil, errors.Wrap(err, "finding ivy program")
	}
	var filter *Row
	if len(c.Children) == 1 {
		row, err := e.executeBitmapCallShard(ctx, qcx, index, c.Children[0], shard)
		if err != nil {
			return nil, err
		}
		filter = row
		if !filter.Any() {
			// no need to actuall run the query for its not operating against any values
			return value.NewVector([]value.Value{}), nil
		}
	}
	//
	pool := memory.NewGoAllocator() // TODO(twg) 2022/09/01 singledton?

	ids := filter.ShardColumns() // needs to be shard columns
	// Fetch index.
	idx := e.Holder.Index(index)
	if idx == nil {
		return nil, newNotFoundError(ErrIndexNotFound, index)
	}
	fname := idx.GetDataFramePath(shard)
	if _, err := os.Stat(fname + ".parquet"); os.IsNotExist(err) {
		return value.NewVector([]value.Value{}), nil
	}

	table, err := readTableParquet(fname)
	if err != nil {
		return nil, err
	}
	defer table.Release()
	df, err := dataframe.NewDataFrameFromTable(pool, table)
	if err != nil {
		return nil, err
	}
	p := dataframe.NewChunkResolver(df.ColumnAt(0))
	var resolver dataframe.Resolver
	resolver = &p
	if filter != nil {
		if len(ids) == 0 {
			return value.NewVector([]value.Value{}), nil
		}
		resolver, err = filterDataframe(resolver, pool, ids)
		if err != nil {
			return nil, err
		}
	}
	conf := getDefaultConfig()
	context, err := ivy.RunArrow(dataframe.NewTableFacade(df), ivyProgram, conf, resolver)
	if err != nil {
		return nil, fmt.Errorf("ivy map error: %w", err)
	}
	return context.Global("_"), nil
}

func readTableParquet(filename string) (arrow.Table, error) {
	r, err := os.Open(filename + ".parquet")
	if err != nil {
		return nil, err
	}

	pf, err := file.NewParquetReader(r)
	if err != nil {
		return nil, err
	}

	reader, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, memory.DefaultAllocator)
	if err != nil {
		return nil, err
	}
	return reader.ReadTable(context.Background())
}

func readTableParquetCtx(ctx context.Context, filename string, mem memory.Allocator) (arrow.Table, error) {
	r, err := os.Open(filename + ".parquet")
	if err != nil {
		return nil, err
	}

	pf, err := file.NewParquetReader(r)
	if err != nil {
		return nil, err
	}

	reader, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, mem)
	if err != nil {
		return nil, err
	}
	return reader.ReadTable(ctx)
}

// ///////////////////////////////////////////////////////
// all the ingest supporting functions
// ///////////////////////////////////////////////////////

func NewShardFile(name string) (*ShardFile, error) {
	if _, err := os.Stat(name + ".parquet"); os.IsNotExist(err) {
		return &ShardFile{dest: name}, nil
	}

	// else read in existing
	table, err := readTableParquet(name)
	if err != nil {
		return nil, err
	}
	return &ShardFile{table: table, schema: table.Schema(), dest: name}, nil
}

type NameType struct {
	Name     string
	DataType arrow.DataType
}
type ChangesetRequest struct {
	ShardIds     []int64 // only shardwidth bits to provide 0 indexing inside shard file
	Columns      []interface{}
	SimpleSchema []NameType
}

// TODO(twg) 2022/09/30 Needs a refactor
func cast(v interface{}) arrow.DataType {
	switch v.(type) {
	case *arrow.Int64Type:
		return arrow.PrimitiveTypes.Int64
	case int64:
		return arrow.PrimitiveTypes.Int64
	case *arrow.Float64Type:
		return arrow.PrimitiveTypes.Float64
	case float64:
		return arrow.PrimitiveTypes.Float64
	default:
		vprint.VV("%T .... %v", v, v)
	}
	return arrow.PrimitiveTypes.Int64
}

func (cr *ChangesetRequest) ArrowSchema() *arrow.Schema {
	fields := make([]arrow.Field, len(cr.SimpleSchema))
	for i := range cr.SimpleSchema {
		fields[i] = arrow.Field{Name: cr.SimpleSchema[i].Name, Type: cast(cr.SimpleSchema[i].DataType)}
	}
	return arrow.NewSchema(fields, nil)
}

type ShardFile struct {
	table      arrow.Table
	schema     *arrow.Schema
	beforeRows int64
	added      int64
	columns    []interface{}
	dest       string
}

func compareSchema(s1, s2 *arrow.Schema) bool {
	if s1 == nil || s2 == nil {
		return false
	}
	if len(s1.Fields()) != len(s2.Fields()) {
		return false
	}
	for i := 0; i < len(s1.Fields()); i++ {
		f1 := s1.Field(i)
		f2 := s2.Field(i)
		if f1.Name != f2.Name {
			return false
		}
		if f1.Type != f2.Type {
			return false
		}
	}
	return true
}

func (sf *ShardFile) EnsureSchema(cs *ChangesetRequest) error {
	schema := cs.ArrowSchema()
	if sf.schema == nil {
		sf.schema = schema
	} else {
		if !compareSchema(sf.schema, schema) {
			vprint.VV("incomeing schema", schema)
			vprint.VV("existing schema", sf.schema)
			return errors.New("dataframe schema's don't match")
		}
	}
	sf.columns = make([]interface{}, len(sf.schema.Fields()))
	return nil
}

func (sf *ShardFile) buildAppenders(maxid int64) {
	if sf.table != nil {
		sf.beforeRows = sf.table.NumRows()
	}
	if maxid < sf.beforeRows {
		// no need to add new rows
		return
	}
	newSize := maxid - sf.beforeRows + 1
	for i := 0; i < len(sf.schema.Fields()); i++ {
		switch sf.schema.Field(i).Type {
		case arrow.PrimitiveTypes.Int64:
			sf.columns[i] = make([]int64, newSize)
		case arrow.PrimitiveTypes.Float64:
			sf.columns[i] = make([]float64, newSize)
		}
	}
	sf.added = newSize
}

// the row offset must be reset to 0 for the slices being appended
func (sf *ShardFile) SetIntValue(col int, row int64, val int64) {
	v := sf.columns[col].([]int64)
	v[row-sf.beforeRows] = val
}

func (sf *ShardFile) SetFloatValue(col int, row int64, val float64) {
	v := sf.columns[col].([]float64)
	v[row-sf.beforeRows] = val
}

func (sf *ShardFile) Process(cs *ChangesetRequest) error {
	err := sf.process(cs)
	if err != nil {
		return err
	}
	rtemp := sf.dest + ".temp"
	err = sf.Save(rtemp)
	if err != nil {
		return err
	}
	return os.Rename(rtemp+".parquet", sf.dest+".parquet")
}

func (sf *ShardFile) process(cs *ChangesetRequest) error {
	offset := 0
	if sf.table != nil {
		column := sf.table.Column(0)
		resolver := dataframe.NewChunkResolver(column)
		for i, rowid := range cs.ShardIds {
			offset = i
			if rowid >= sf.table.NumRows() {
				break
			}
			chunk, l := resolver.Resolve(int(rowid))
			for col := 0; col < len(sf.schema.Fields()); col++ {
				column := sf.table.Column(col)
				switch column.DataType() {
				case arrow.PrimitiveTypes.Int64:
					v := column.Data().Chunk(chunk).(*array.Int64).Int64Values()
					v[l] = cs.Columns[col].([]int64)[i]
				case arrow.PrimitiveTypes.Float64:
					v := column.Data().Chunk(chunk).(*array.Float64).Float64Values()
					v[l] = cs.Columns[col].([]float64)[i]
				default:
					panic(fmt.Sprintf("Unknown Type %v", column.DataType()))
				}
			}
		}
	}
	max := cs.ShardIds[len(cs.ShardIds)-1]
	sf.buildAppenders(max)
	// need to check if only replace and no apend
	if sf.added > 0 {
		for i, rowid := range cs.ShardIds[offset:] {
			i += offset

			for col := 0; col < len(sf.schema.Fields()); col++ {
				switch sf.schema.Field(col).Type {
				case arrow.PrimitiveTypes.Int64:
					sf.SetIntValue(col, rowid, cs.Columns[col].([]int64)[i])
				case arrow.PrimitiveTypes.Float64:
					sf.SetFloatValue(col, rowid, cs.Columns[col].([]float64)[i])
				default:
					panic(fmt.Sprintf("2 Unknown Type %v", sf.schema.Field(col).Type))
				}
			}
		}
	}

	return nil
}

func (sf *ShardFile) Save(name string) error {
	parts := make([]arrow.Array, 0)
	mem := memory.NewGoAllocator()
	for col := 0; col < len(sf.schema.Fields()); col++ {
		chunks := make([]arrow.Array, 0)
		if sf.table != nil {
			// we append if there was existing parquet file
			column := sf.table.Column(col)
			chunks = append(chunks, column.Data().Chunks()...)
		}
		switch sf.schema.Field(col).Type {
		case arrow.PrimitiveTypes.Int64:
			// case *arrow.Int64Type:
			if sf.added > 0 {
				ibuild := array.NewInt64Builder(mem)
				ibuild.AppendValues(sf.columns[col].([]int64), nil) // TODO(twg) 2022/09/28 need to handle null
				newChunk := ibuild.NewArray()
				chunks = append(chunks, newChunk)
			}
			record, err := array.Concatenate(chunks, mem)
			if err != nil {
				return err
			}
			parts = append(parts, record)
		case arrow.PrimitiveTypes.Float64:
			// case *arrow.Float64Type:
			if sf.added > 0 {
				fbuild := array.NewFloat64Builder(mem)
				fbuild.AppendValues(sf.columns[col].([]float64), nil) // TODO(twg) 2022/09/28 need to handle null
				newChunk := fbuild.NewArray()
				chunks = append(chunks, newChunk)
			}
			record, err := array.Concatenate(chunks, mem)
			if err != nil {
				return err
			}
			parts = append(parts, record)
		default:
			vprint.VV("UNKNOWN %T", sf.schema.Field(col).Type)
		}
	}
	rec := array.NewRecord(sf.schema, parts, sf.beforeRows+sf.added)
	df, err := dataframe.NewDataFrameFromRecord(mem, rec)
	if err != nil {
		return err
	}
	// confirm change
	w, err := os.Create(name + ".parquet")
	if err != nil {
		return err
	}

	err = df.ToParquet(w, 1024)
	if err != nil {
		return err
	}
	w.Close()
	return nil
}

// TODO(twg) 2022/10/03 Not a huge fan of the global variable will look at adding to executor structure
// when dataframe is fully integrated
var (
	dataframeShardLocks map[uint64]*sync.Mutex
	muWriteDataframe    sync.Mutex
)

func init() {
	dataframeShardLocks = make(map[uint64]*sync.Mutex)
}

func getDataframeWritelock(shard uint64) *sync.Mutex {
	muWriteDataframe.Lock()
	defer muWriteDataframe.Unlock()
	lock, ok := dataframeShardLocks[shard]
	if ok {
		return lock
	}
	newLock := sync.Mutex{}
	dataframeShardLocks[shard] = &newLock
	return &newLock
}

func (api *API) ApplyDataframeChangeset(ctx context.Context, index string, cs *ChangesetRequest, shard uint64) error {
	// TODO(twg) 2022/09/29 need to validate api call
	idx := api.Holder().Index(index)

	// check if dataframe exists
	fname := idx.GetDataFramePath(shard)

	// only 1 shard writer allowed at at time so wait for it to be available
	mu := getDataframeWritelock(shard)
	mu.Lock()
	defer mu.Unlock()
	shardFile, err := NewShardFile(fname)
	if err != nil {
		return err
	}

	err = shardFile.EnsureSchema(cs)
	if err != nil {
		return err
	}

	return shardFile.Process(cs)
}

type column struct {
	Name string
	Type string
}

func (api *API) GetDataframeSchema(ctx context.Context, indexName string) (interface{}, error) {
	idx, err := api.Index(ctx, indexName)
	if err != nil {
		return nil, err
	}
	base := idx.DataframesPath()
	dir, _ := os.Open(base)
	files, _ := dir.Readdir(0)
	parts := make([]column, 0)
	for i := range files {
		file := files[i]
		name := file.Name()
		if strings.HasSuffix(name, ".parquet") {
			// strip off the parquet extenison
			name = strings.TrimSuffix(name, filepath.Ext(name))
			// read the parquet file and extract the schema
			table, err := readTableParquet(filepath.Join(base, name))
			if err != nil {
				return nil, err
			}
			for i := 0; i < int(table.NumCols()); i++ {
				col := table.Column(i)
				part := column{Name: col.Name(), Type: col.DataType().String()}
				parts = append(parts, part)
			}
			break // only go on first file
		}
	}
	return parts, nil
}
