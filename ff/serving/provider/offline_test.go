// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package provider

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOfflineStores(t *testing.T) {
	err := godotenv.Load(".env")
	if err != nil {
		fmt.Println(err)
	}
	var postgresConfig = PostgresConfig{
		Host:     "localhost",
		Port:     "5432",
		Database: os.Getenv("POSTGRES_DB"),
		Username: os.Getenv("POSTGRES_USER"),
		Password: os.Getenv("POSTGRES_PASSWORD"),
	}
	serialPGConfig := postgresConfig.Serialize()
	os.Setenv("TZ", "UTC")
	snowFlakeDatabase := strings.ToUpper(uuid.NewString())
	t.Log("Snowflake Database: ", snowFlakeDatabase)
	var snowflakeConfig = SnowflakeConfig{
		Username:     os.Getenv("SNOWFLAKE_USERNAME"),
		Password:     os.Getenv("SNOWFLAKE_PASSWORD"),
		Organization: os.Getenv("SNOWFLAKE_ORG"),
		Account:      os.Getenv("SNOWFLAKE_ACCOUNT"),
		Database:     snowFlakeDatabase,
	}
	serialSFConfig := snowflakeConfig.Serialize()
	if err := createSnowflakeDatabase(snowflakeConfig); err != nil {
		t.Fatalf("%v", err)
	}
	defer destroySnowflakeDatabase(snowflakeConfig)

	testFns := map[string]func(*testing.T, OfflineStore){
		"CreateGetTable":          testCreateGetOfflineTable,
		"TableAlreadyExists":      testOfflineTableAlreadyExists,
		"TableNotFound":           testOfflineTableNotFound,
		"InvalidResourceIDs":      testInvalidResourceIDs,
		"Materializations":        testMaterializations,
		"InvalidResourceRecord":   testWriteInvalidResourceRecord,
		"InvalidMaterialization":  testInvalidMaterialization,
		"MaterializeUnknown":      testMaterializeUnknown,
		"MaterializationNotFound": testMaterializationNotFound,
		"TrainingSets":            testTrainingSet,
		"TrainingSetInvalidID":    testGetTrainingSetInvalidResourceID,
		"GetUnknownTrainingSet":   testGetUnkonwnTrainingSet,
		"InvalidTrainingSetDefs":  testInvalidTrainingSetDefs,
		"LabelTableNotFound":      testLabelTableNotFound,
		"FeatureTableNotFound":    testFeatureTableNotFound,
		"TrainingDefShorthand":    testTrainingSetDefShorthand,
	}
	testSQLFns := map[string]func(*testing.T, OfflineStore){
		"PrimaryTableCreate":          testPrimaryCreateTable,
		"PrimaryTableWrite":           testPrimaryTableWrite,
		"Transformation":              testTransform,
		"TransformToFeature":          testTransformCreateFeature,
		"SQLValidity":                 testSQLValidity,
		"CreateDuplicatePrimaryTable": testCreateDuplicatePrimaryTable,
		"ChainTransformations":        testChainTransform,
	}
	testList := []struct {
		t               Type
		c               SerializedConfig
		integrationTest bool
	}{
		{MemoryOffline, []byte{}, false},
		{PostgresOffline, serialPGConfig, true},
		{SnowflakeOffline, serialSFConfig, true},
	}
	for _, testItem := range testList {
		if testing.Short() && testItem.integrationTest {
			t.Logf("Skipping %s, because it is an integration test", testItem.t)
			continue
		}
		for name, fn := range testFns {
			provider, err := Get(testItem.t, testItem.c)
			if err != nil {
				t.Fatalf("Failed to get provider %s: %s", testItem.t, err)
			}
			store, err := provider.AsOfflineStore()
			if err != nil {
				t.Fatalf("Failed to use provider %s as OfflineStore: %s", testItem.t, err)
			}
			testName := fmt.Sprintf("%s_%s", testItem.t, name)
			t.Run(testName, func(t *testing.T) {
				fn(t, store)
			})
		}
		for name, fn := range testSQLFns {
			if testItem.t == MemoryOffline {
				continue
			}
			provider, err := Get(testItem.t, testItem.c)
			if err != nil {
				t.Fatalf("Failed to get provider %s: %s", testItem.t, err)
			}
			store, err := provider.AsOfflineStore()
			if err != nil {
				t.Logf("Cannot use provider %s as SQLOfflineStore: %s", testItem.t, err)
				continue
			}
			testName := fmt.Sprintf("%s_%s", testItem.t, name)
			t.Run(testName, func(t *testing.T) {
				fn(t, store)
			})
		}
	}
}

func createSnowflakeDatabase(c SnowflakeConfig) error {
	url := fmt.Sprintf("%s:%s@%s-%s", c.Username, c.Password, c.Organization, c.Account)
	db, err := sql.Open("snowflake", url)
	if err != nil {
		return err
	}
	databaseQuery := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", sanitize(c.Database))
	if _, err := db.Exec(databaseQuery); err != nil {
		return err
	}
	return nil
}

func destroySnowflakeDatabase(c SnowflakeConfig) error {
	url := fmt.Sprintf("%s:%s@%s-%s", c.Username, c.Password, c.Organization, c.Account)
	db, err := sql.Open("snowflake", url)
	if err != nil {
		return err
	}
	databaseQuery := fmt.Sprintf("DROP DATABASE IF EXISTS %s", sanitize(c.Database))
	if _, err := db.Exec(databaseQuery); err != nil {
		return err
	}
	return nil
}

func randomID(types ...OfflineResourceType) ResourceID {
	var t OfflineResourceType
	if len(types) == 0 {
		t = NoType
	} else if len(types) == 1 {
		t = types[0]
	} else {
		t = types[rand.Intn(len(types))]
	}
	return ResourceID{
		Name:    uuid.NewString(),
		Variant: uuid.NewString(),
		Type:    t,
	}
}

func randomFeatureID() ResourceID {
	return ResourceID{
		Name:    uuid.NewString(),
		Variant: uuid.NewString(),
		Type:    Feature,
	}
}

func randomLabelID() ResourceID {
	return ResourceID{
		Name:    uuid.NewString(),
		Variant: uuid.NewString(),
		Type:    Label,
	}
}

func testCreateGetOfflineTable(t *testing.T, store OfflineStore) {
	id := randomID(Feature, Label)
	schema := TableSchema{
		Columns: []TableColumn{
			{Name: "entity", ValueType: String},
			{Name: "value", ValueType: Int},
			{Name: "ts", ValueType: Timestamp},
		},
	}
	if tab, err := store.CreateResourceTable(id, schema); tab == nil || err != nil {
		t.Fatalf("Failed to create table: %s", err)
	}
	if tab, err := store.GetResourceTable(id); tab == nil || err != nil {
		t.Fatalf("Failed to get table: %s", err)
	}
}

func testOfflineTableAlreadyExists(t *testing.T, store OfflineStore) {
	id := randomID(Feature, Label)
	schema := TableSchema{
		Columns: []TableColumn{
			{Name: "", ValueType: String},
			{Name: "", ValueType: Int},
			{Name: "", ValueType: Timestamp},
		},
	}
	if _, err := store.CreateResourceTable(id, schema); err != nil {
		t.Fatalf("Failed to create table: %s", err)
	}
	if _, err := store.CreateResourceTable(id, schema); err == nil {
		t.Fatalf("Succeeded in creating table twice")
	} else if casted, valid := err.(*TableAlreadyExists); !valid {
		t.Fatalf("Wrong error for table already exists: %T", err)
	} else if casted.Error() == "" {
		t.Fatalf("TableAlreadyExists has empty error message")
	}
}

func testOfflineTableNotFound(t *testing.T, store OfflineStore) {
	id := randomID(Feature, Label)
	if _, err := store.GetResourceTable(id); err == nil {
		t.Fatalf("Succeeded in getting non-existant table")
	} else if casted, valid := err.(*TableNotFound); !valid {
		t.Fatalf("Wrong error for table not found: %T", err)
	} else if casted.Error() == "" {
		t.Fatalf("TableNotFound has empty error message")
	}
}

func testMaterializations(t *testing.T, store OfflineStore) {
	type TestCase struct {
		WriteRecords             []ResourceRecord
		Schema                   TableSchema
		ExpectedRows             int64
		SegmentStart, SegmentEnd int64
		ExpectedSegment          []ResourceRecord
	}

	schemaInt := TableSchema{
		Columns: []TableColumn{
			{Name: "entity", ValueType: String},
			{Name: "value", ValueType: Int},
			{Name: "ts", ValueType: Timestamp},
		},
	}
	tests := map[string]TestCase{
		"Empty": {
			WriteRecords:    []ResourceRecord{},
			Schema:          schemaInt,
			SegmentStart:    0,
			SegmentEnd:      0,
			ExpectedSegment: []ResourceRecord{},
		},
		"NoOverlap": {
			WriteRecords: []ResourceRecord{
				{Entity: "a", Value: 1},
				{Entity: "b", Value: 2},
				{Entity: "c", Value: 3},
			},
			Schema:       schemaInt,
			ExpectedRows: 3,
			SegmentStart: 0,
			SegmentEnd:   3,
			// Have to expect time.UnixMilli(0).UTC() as it is the default value
			// if a resource does not have a set timestamp
			ExpectedSegment: []ResourceRecord{
				{Entity: "a", Value: 1, TS: time.UnixMilli(0).UTC()},
				{Entity: "b", Value: 2, TS: time.UnixMilli(0).UTC()},
				{Entity: "c", Value: 3, TS: time.UnixMilli(0).UTC()},
			},
		},
		"SubSegmentNoOverlap": {
			WriteRecords: []ResourceRecord{
				{Entity: "a", Value: 1},
				{Entity: "b", Value: 2},
				{Entity: "c", Value: 3},
			},
			Schema:       schemaInt,
			ExpectedRows: 3,
			SegmentStart: 1,
			SegmentEnd:   2,
			ExpectedSegment: []ResourceRecord{
				{Entity: "b", Value: 2, TS: time.UnixMilli(0).UTC()},
			},
		},
		"SimpleOverwrite": {
			WriteRecords: []ResourceRecord{
				{Entity: "a", Value: 1},
				{Entity: "b", Value: 2},
				{Entity: "c", Value: 3},
				{Entity: "a", Value: 4},
			},
			Schema:       schemaInt,
			ExpectedRows: 3,
			SegmentStart: 0,
			SegmentEnd:   3,
			ExpectedSegment: []ResourceRecord{
				{Entity: "a", Value: 4, TS: time.UnixMilli(0).UTC()},
				{Entity: "b", Value: 2, TS: time.UnixMilli(0).UTC()},
				{Entity: "c", Value: 3, TS: time.UnixMilli(0).UTC()},
			},
		},
		// Added .UTC() b/c DeepEqual checks the timezone field of time.Time which can vary, resulting in false failures
		// during tests even if time is correct
		"SimpleChanges": {
			WriteRecords: []ResourceRecord{
				{Entity: "a", Value: 1, TS: time.UnixMilli(0).UTC()},
				{Entity: "b", Value: 2, TS: time.UnixMilli(0).UTC()},
				{Entity: "c", Value: 3, TS: time.UnixMilli(0).UTC()},
				{Entity: "a", Value: 4, TS: time.UnixMilli(1).UTC()},
			},
			Schema:       schemaInt,
			ExpectedRows: 3,
			SegmentStart: 0,
			SegmentEnd:   3,
			ExpectedSegment: []ResourceRecord{
				{Entity: "a", Value: 4, TS: time.UnixMilli(1).UTC()},
				{Entity: "b", Value: 2, TS: time.UnixMilli(0).UTC()},
				{Entity: "c", Value: 3, TS: time.UnixMilli(0).UTC()},
			},
		},
		"OutOfOrderWrites": {
			WriteRecords: []ResourceRecord{
				{Entity: "a", Value: 1, TS: time.UnixMilli(10).UTC()},
				{Entity: "b", Value: 2, TS: time.UnixMilli(3).UTC()},
				{Entity: "c", Value: 3, TS: time.UnixMilli(7).UTC()},
				{Entity: "c", Value: 9, TS: time.UnixMilli(5).UTC()},
				{Entity: "a", Value: 4, TS: time.UnixMilli(1).UTC()},
			},
			Schema:       schemaInt,
			ExpectedRows: 3,
			SegmentStart: 0,
			SegmentEnd:   3,
			ExpectedSegment: []ResourceRecord{
				{Entity: "a", Value: 1, TS: time.UnixMilli(10).UTC()},
				{Entity: "b", Value: 2, TS: time.UnixMilli(3).UTC()},
				{Entity: "c", Value: 3, TS: time.UnixMilli(7).UTC()},
			},
		},
		"OutOfOrderOverwrites": {
			WriteRecords: []ResourceRecord{
				{Entity: "a", Value: 1, TS: time.UnixMilli(10).UTC()},
				{Entity: "b", Value: 2, TS: time.UnixMilli(3).UTC()},
				{Entity: "c", Value: 3, TS: time.UnixMilli(7).UTC()},
				{Entity: "c", Value: 9, TS: time.UnixMilli(5).UTC()},
				{Entity: "b", Value: 12, TS: time.UnixMilli(2).UTC()},
				{Entity: "a", Value: 4, TS: time.UnixMilli(1).UTC()},
				{Entity: "b", Value: 9, TS: time.UnixMilli(3).UTC()},
			},
			Schema:       schemaInt,
			ExpectedRows: 3,
			SegmentStart: 0,
			SegmentEnd:   3,
			ExpectedSegment: []ResourceRecord{
				{Entity: "a", Value: 1, TS: time.UnixMilli(10).UTC()},
				{Entity: "b", Value: 9, TS: time.UnixMilli(3).UTC()},
				{Entity: "c", Value: 3, TS: time.UnixMilli(7).UTC()},
			},
		},
	}
	testMaterialization := func(t *testing.T, mat Materialization, test TestCase) {
		if numRows, err := mat.NumRows(); err != nil {
			t.Fatalf("Failed to get num rows: %s", err)
		} else if numRows != test.ExpectedRows {
			t.Fatalf("Num rows not equal %d %d", numRows, test.ExpectedRows)
		}
		seg, err := mat.IterateSegment(test.SegmentStart, test.SegmentEnd)
		if err != nil {
			t.Fatalf("Failed to create segment: %s", err)
		}
		i := 0
		for seg.Next() {
			actual := seg.Value()
			expected := test.ExpectedSegment[i]
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("Value not equal in materialization: %v %v", actual, expected)
			}
			i++
		}
		if err := seg.Err(); err != nil {
			t.Fatalf("Iteration failed: %s", err)
		}
		if i < len(test.ExpectedSegment) {
			t.Fatalf("Segment is too small: %d", i)
		}
	}
	runTestCase := func(t *testing.T, test TestCase) {
		id := randomID(Feature)
		table, err := store.CreateResourceTable(id, test.Schema)
		if err != nil {
			t.Fatalf("Failed to create table: %s", err)
		}
		for _, rec := range test.WriteRecords {
			if err := table.Write(rec); err != nil {
				t.Fatalf("Failed to write record %v: %s", rec, err)
			}
		}
		mat, err := store.CreateMaterialization(id)
		if err != nil {
			t.Fatalf("Failed to create materialization: %s", err)
		}
		testMaterialization(t, mat, test)
		getMat, err := store.GetMaterialization(mat.ID())
		if err != nil {
			t.Fatalf("Failed to get materialization: %s", err)
		}
		testMaterialization(t, getMat, test)
		if err := store.DeleteMaterialization(mat.ID()); err != nil {
			t.Fatalf("Failed to delete materialization: %s", err)
		}
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			runTestCase(t, test)
		})
	}

}

func testWriteInvalidResourceRecord(t *testing.T, store OfflineStore) {
	id := randomID(Feature)
	schema := TableSchema{
		Columns: []TableColumn{
			{Name: "entity", ValueType: String},
			{Name: "value", ValueType: Int},
			{Name: "ts", ValueType: Timestamp},
		},
	}
	table, err := store.CreateResourceTable(id, schema)
	if err != nil {
		t.Fatalf("Failed to create table: %s", err)
	}
	if err := table.Write(ResourceRecord{}); err == nil {
		t.Fatalf("Succeeded in writing invalid resource record")
	}
}

func testInvalidMaterialization(t *testing.T, store OfflineStore) {
	id := randomID(Label)
	schema := TableSchema{
		Columns: []TableColumn{
			{Name: "entity", ValueType: String},
			{Name: "value", ValueType: Int},
			{Name: "ts", ValueType: Timestamp},
		},
	}
	if _, err := store.CreateResourceTable(id, schema); err != nil {
		t.Fatalf("Failed to create table: %s", err)
	}
	if _, err := store.CreateMaterialization(id); err == nil {
		t.Fatalf("Succeeded in materializing label")
	}
}

func testMaterializeUnknown(t *testing.T, store OfflineStore) {
	id := randomID(Feature)
	if _, err := store.CreateMaterialization(id); err == nil {
		t.Fatalf("Succeeded in materializing uninitialized resource")
	}
}

func testMaterializationNotFound(t *testing.T, store OfflineStore) {

	id := MaterializationID(uuid.NewString())
	_, err := store.GetMaterialization(id)
	if err == nil {
		t.Fatalf("Succeeded in getting uninitialized materialization")
	}
	err = store.DeleteMaterialization(id)
	if err == nil {
		t.Fatalf("Succeeded in deleting uninitialized materialization")
	}
	var notFoundErr *MaterializationNotFound
	if validCast := errors.As(err, &notFoundErr); !validCast {
		t.Fatalf("Wrong Error type for materialization not found: %T", err)
	}
	if notFoundErr.Error() == "" {
		t.Fatalf("MaterializationNotFound Error not implemented")
	}
}

func testInvalidResourceIDs(t *testing.T, store OfflineStore) {
	schema := TableSchema{
		Columns: []TableColumn{
			{Name: "entity", ValueType: String},
			{Name: "value", ValueType: Int},
			{Name: "ts", ValueType: Timestamp},
		},
	}
	invalidIds := []ResourceID{
		{Type: Feature},
		{Name: uuid.NewString()},
	}
	for _, id := range invalidIds {
		if _, err := store.CreateResourceTable(id, schema); err == nil {
			t.Fatalf("Succeeded in creating invalid ResourceID: %v", id)
		}
	}
}

func testTrainingSet(t *testing.T, store OfflineStore) {
	type expectedTrainingRow struct {
		Features []interface{}
		Label    interface{}
	}
	type TestCase struct {
		FeatureRecords [][]ResourceRecord
		LabelRecords   []ResourceRecord
		ExpectedRows   []expectedTrainingRow
		FeatureSchema  []TableSchema
		LabelSchema    TableSchema
	}

	tests := map[string]TestCase{
		"Empty": {
			FeatureRecords: [][]ResourceRecord{
				// One feature with no records.
				{},
			},
			LabelRecords:  []ResourceRecord{},
			FeatureSchema: []TableSchema{{}},
			// No rows expected
			ExpectedRows: []expectedTrainingRow{},
		},
		"SimpleJoin": {
			FeatureRecords: [][]ResourceRecord{
				{
					{Entity: "a", Value: 1},
					{Entity: "b", Value: 2},
					{Entity: "c", Value: 3},
				},
				{
					{Entity: "a", Value: "red"},
					{Entity: "b", Value: "green"},
					{Entity: "c", Value: "blue"},
				},
			},
			FeatureSchema: []TableSchema{
				{
					Columns: []TableColumn{
						{Name: "entity", ValueType: String},
						{Name: "value", ValueType: Int},
						{Name: "label", ValueType: Bool},
					},
				},
				{
					Columns: []TableColumn{
						{Name: "entity", ValueType: String},
						{Name: "value", ValueType: String},
						{Name: "label", ValueType: Bool},
					},
				},
			},
			LabelRecords: []ResourceRecord{
				{Entity: "a", Value: true},
				{Entity: "b", Value: false},
				{Entity: "c", Value: true},
			},
			LabelSchema: TableSchema{
				Columns: []TableColumn{
					{Name: "entity", ValueType: String},
					{Name: "value", ValueType: Bool},
				},
			},
			ExpectedRows: []expectedTrainingRow{
				{
					Features: []interface{}{
						1,
						"red",
					},
					Label: true,
				},
				{
					Features: []interface{}{
						2,
						"green",
					},
					Label: false,
				},
				{
					Features: []interface{}{
						3,
						"blue",
					},
					Label: true,
				},
			},
		},
		"ComplexJoin": {
			FeatureRecords: [][]ResourceRecord{
				// Overwritten feature.
				{
					{Entity: "a", Value: 1},
					{Entity: "b", Value: 2},
					{Entity: "c", Value: 3},
					{Entity: "a", Value: 4},
				},
				// Feature didn't exist before label
				{
					{Entity: "a", Value: "doesnt exist", TS: time.UnixMilli(11)},
				},
				// Feature didn't change after label
				{
					{Entity: "c", Value: "real value first", TS: time.UnixMilli(5)},
					{Entity: "c", Value: "real value second", TS: time.UnixMilli(5)},
					{Entity: "c", Value: "overwritten", TS: time.UnixMilli(4)},
				},
				// Different feature values for different TS.
				{
					{Entity: "b", Value: "first", TS: time.UnixMilli(3)},
					{Entity: "b", Value: "second", TS: time.UnixMilli(4)},
					{Entity: "b", Value: "third", TS: time.UnixMilli(8)},
				},
				// Empty feature.
				{},
			},
			FeatureSchema: []TableSchema{
				{
					Columns: []TableColumn{
						{Name: "entity", ValueType: String},
						{Name: "value", ValueType: Int},
					},
				},
				{
					Columns: []TableColumn{
						{Name: "entity", ValueType: String},
						{Name: "value", ValueType: String},
					},
				},
				{
					Columns: []TableColumn{
						{Name: "entity", ValueType: String},
						{Name: "value", ValueType: String},
					},
				},
				{
					Columns: []TableColumn{
						{Name: "entity", ValueType: String},
						{Name: "value", ValueType: String},
					},
				},
				{
					Columns: []TableColumn{
						{},
					},
				},
			},
			LabelRecords: []ResourceRecord{
				{Entity: "a", Value: 1, TS: time.UnixMilli(10)},
				{Entity: "b", Value: 9, TS: time.UnixMilli(3)},
				{Entity: "b", Value: 5, TS: time.UnixMilli(5)},
				{Entity: "c", Value: 3, TS: time.UnixMilli(7)},
			},
			LabelSchema: TableSchema{
				Columns: []TableColumn{
					{Name: "entity", ValueType: String},
					{Name: "value", ValueType: Int},
				},
			},
			ExpectedRows: []expectedTrainingRow{
				{
					Features: []interface{}{
						4, nil, nil, nil, nil,
					},
					Label: 1,
				},
				{
					Features: []interface{}{
						2, nil, nil, "first", nil,
					},
					Label: 9,
				},
				{
					Features: []interface{}{
						2, nil, nil, "second", nil,
					},
					Label: 5,
				},
				{
					Features: []interface{}{
						3, nil, "real value second", nil, nil,
					},
					Label: 3,
				},
			},
		},
	}
	runTestCase := func(t *testing.T, test TestCase) {
		featureIDs := make([]ResourceID, len(test.FeatureRecords))
		for i, recs := range test.FeatureRecords {
			id := randomID(Feature)
			featureIDs[i] = id
			table, err := store.CreateResourceTable(id, test.FeatureSchema[i])
			if err != nil {
				t.Fatalf("Failed to create table: %s", err)
			}
			for _, rec := range recs {
				if err := table.Write(rec); err != nil {
					t.Fatalf("Failed to write record %v: %v", rec, err)
				}
			}
		}
		labelID := randomID(Label)
		labelTable, err := store.CreateResourceTable(labelID, test.LabelSchema)
		if err != nil {
			t.Fatalf("Failed to create table: %s", err)
		}
		for _, rec := range test.LabelRecords {
			if err := labelTable.Write(rec); err != nil {
				t.Fatalf("Failed to write record %v", rec)
			}
		}
		def := TrainingSetDef{
			ID:       randomID(TrainingSet),
			Label:    labelID,
			Features: featureIDs,
		}
		if err := store.CreateTrainingSet(def); err != nil {
			t.Fatalf("Failed to create training set: %s", err)
		}
		iter, err := store.GetTrainingSet(def.ID)
		if err != nil {
			t.Fatalf("Failed to get training set: %s", err)
		}
		i := 0
		expectedRows := test.ExpectedRows
		for iter.Next() {
			realRow := expectedTrainingRow{
				Features: iter.Features(),
				Label:    iter.Label(),
			}
			// Row order isn't guaranteed, we make sure one row is equivalent
			// then we delete that row. This is ineffecient, but these test
			// cases should all be small enough not to matter.
			found := false
			for i, expRow := range expectedRows {
				if reflect.DeepEqual(realRow, expRow) {
					found = true
					lastIdx := len(expectedRows) - 1
					// Swap the record that we've found to the end, then shrink the slice to not include it.
					// This is essentially a delete operation expect that it re-orders the slice.
					expectedRows[i], expectedRows[lastIdx] = expectedRows[lastIdx], expectedRows[i]
					expectedRows = expectedRows[:lastIdx]
					break
				}
			}
			if !found {
				t.Fatalf("Unexpected training row: %v, expected %v", realRow, expectedRows)
			}
			i++
		}
		if err := iter.Err(); err != nil {
			t.Fatalf("Failed to iterate training set: %s", err)
		}
		if len(test.ExpectedRows) != i {
			t.Fatalf("Training set has different number of rows %d %d", len(test.ExpectedRows), i)
		}
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			runTestCase(t, test)
		})
	}
}

func testGetTrainingSetInvalidResourceID(t *testing.T, store OfflineStore) {
	id := randomID(Feature)
	if _, err := store.GetTrainingSet(id); err == nil {
		t.Fatalf("Succeeded in getting invalid training set ResourceID")
	}
}

func testGetUnkonwnTrainingSet(t *testing.T, store OfflineStore) {
	// This should default to TrainingSet
	id := randomID(NoType)
	if _, err := store.GetTrainingSet(id); err == nil {
		t.Fatalf("Succeeded in getting unknown training set ResourceID")
	} else if _, valid := err.(*TrainingSetNotFound); !valid {
		t.Fatalf("Wrong error for training set not found: %T", err)
	} else if err.Error() == "" {
		t.Fatalf("Training set not found error msg not set")
	}
}

func testInvalidTrainingSetDefs(t *testing.T, store OfflineStore) {
	invalidDefs := map[string]TrainingSetDef{
		"WrongTSType": TrainingSetDef{
			ID:    randomID(Feature),
			Label: randomID(Label),
			Features: []ResourceID{
				randomID(Feature),
				randomID(Feature),
				randomID(Feature),
			},
		},
		"WrongLabelType": TrainingSetDef{
			ID:    randomID(TrainingSet),
			Label: randomID(Feature),
			Features: []ResourceID{
				randomID(Feature),
				randomID(Feature),
				randomID(Feature),
			},
		},
		"WrongFeatureType": TrainingSetDef{
			ID:    randomID(TrainingSet),
			Label: randomID(Label),
			Features: []ResourceID{
				randomID(Feature),
				randomID(Label),
				randomID(Feature),
			},
		},
		"NoFeatures": TrainingSetDef{
			ID:       randomID(TrainingSet),
			Label:    randomID(Label),
			Features: []ResourceID{},
		},
	}
	for name, def := range invalidDefs {
		t.Run(name, func(t *testing.T) {
			if err := store.CreateTrainingSet(def); err == nil {
				t.Fatalf("Succeeded to create invalid def")
			}
		})
	}
}

func testLabelTableNotFound(t *testing.T, store OfflineStore) {
	featureID := randomID(Feature)
	schema := TableSchema{
		Columns: []TableColumn{
			{Name: "entity", ValueType: String},
			{Name: "value", ValueType: Int},
			{Name: "ts", ValueType: Timestamp},
		},
	}
	if _, err := store.CreateResourceTable(featureID, schema); err != nil {
		t.Fatalf("Failed to create table: %s", err)
	}
	def := TrainingSetDef{
		ID:    randomID(TrainingSet),
		Label: randomID(Label),
		Features: []ResourceID{
			featureID,
		},
	}
	if err := store.CreateTrainingSet(def); err == nil {
		t.Fatalf("Succeeded in creating training set with unknown label")
	}
}

func testFeatureTableNotFound(t *testing.T, store OfflineStore) {
	labelID := randomID(Label)
	schema := TableSchema{
		Columns: []TableColumn{
			{Name: "entity", ValueType: String},
			{Name: "value", ValueType: Int},
			{Name: "ts", ValueType: Timestamp},
		},
	}
	if _, err := store.CreateResourceTable(labelID, schema); err != nil {
		t.Fatalf("Failed to create table: %s", err)
	}
	def := TrainingSetDef{
		ID:    randomID(TrainingSet),
		Label: labelID,
		Features: []ResourceID{
			randomID(Feature),
		},
	}
	if err := store.CreateTrainingSet(def); err == nil {
		t.Fatalf("Succeeded in creating training set with unknown feature")
	}
}

func testTrainingSetDefShorthand(t *testing.T, store OfflineStore) {
	schema := TableSchema{
		Columns: []TableColumn{
			{Name: "entity", ValueType: String},
			{Name: "value", ValueType: String},
			{Name: "ts", ValueType: Timestamp},
		},
	}
	fId := randomID(Feature)
	fTable, err := store.CreateResourceTable(fId, schema)
	if err != nil {
		t.Fatalf("Failed to create table: %s", err)
	}
	fTable.Write(ResourceRecord{Entity: "a", Value: "feature"})
	lId := randomID(Label)
	lTable, err := store.CreateResourceTable(lId, schema)
	if err != nil {
		t.Fatalf("Failed to create table: %s", err)
	}
	lTable.Write(ResourceRecord{Entity: "a", Value: "label"})
	// TrainingSetDef can be done in shorthand without types. Their types should
	// be set automatically by the check() function.
	lId.Type = NoType
	fId.Type = NoType
	def := TrainingSetDef{
		ID:       randomID(NoType),
		Label:    lId,
		Features: []ResourceID{fId},
	}
	if err := store.CreateTrainingSet(def); err != nil {
		t.Fatalf("Failed to create training set: %s", err)
	}
}

func testPrimaryCreateTable(t *testing.T, store OfflineStore) {
	type TestCreateCase struct {
		Rec         ResourceID
		Schema      TableSchema
		ExpectError bool
		ExpectValue PrimaryTable
	}
	testCreate := map[string]TestCreateCase{
		"InvalidLabelResource": {
			Rec: ResourceID{
				Name: uuid.NewString(),
				Type: Label,
			},
			Schema: TableSchema{
				Columns: []TableColumn{},
			},
			ExpectError: true,
			ExpectValue: nil,
		},
		"InvalidFeatureResource": {
			Rec: ResourceID{
				Name: uuid.NewString(),
				Type: Feature,
			},
			Schema: TableSchema{
				Columns: []TableColumn{},
			},
			ExpectError: true,
			ExpectValue: nil,
		},
		"InvalidTrainingSetResource": {
			Rec: ResourceID{
				Name: uuid.NewString(),
				Type: TrainingSet,
			},
			Schema: TableSchema{
				Columns: []TableColumn{},
			},
			ExpectError: true,
			ExpectValue: nil,
		},
		"InvalidColumns": {
			Rec: ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			},
			Schema: TableSchema{
				Columns: []TableColumn{},
			},
			ExpectError: true,
			ExpectValue: nil,
		},
		"ValidPrimaryTable": {
			Rec: ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			},
			Schema: TableSchema{
				Columns: []TableColumn{
					{Name: "entity", ValueType: String},
					{Name: "int", ValueType: Int},
					{Name: "bool", ValueType: Bool},
					{Name: "string", ValueType: String},
					{Name: "float", ValueType: Float32},
				},
			},
			ExpectError: false,
			ExpectValue: nil,
		},
	}

	testPrimary := func(t *testing.T, c TestCreateCase, store OfflineStore) {
		_, err := store.CreatePrimaryTable(c.Rec, c.Schema)
		if err != nil && c.ExpectError == false {
			t.Fatalf("Did not expected error, received: %v", err)
		}
	}
	for name, test := range testCreate {
		t.Run(name, func(t *testing.T) {
			testPrimary(t, test, store)
		})
	}
}

func testPrimaryTableWrite(t *testing.T, store OfflineStore) {
	type TestCase struct {
		Rec         ResourceID
		Schema      TableSchema
		Records     []GenericRecord
		ExpectError bool
		Expected    []GenericRecord
	}

	tests := map[string]TestCase{
		"NoColumnEmpty": {
			Rec: ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			},
			Schema: TableSchema{
				Columns: []TableColumn{
					{Name: "entity", ValueType: String},
					{Name: "value", ValueType: Int},
					{Name: "timestamp", ValueType: Timestamp},
				},
			},
			Records:     []GenericRecord{},
			ExpectError: false,
			Expected:    []GenericRecord{},
		},
		"SimpleColumnEmpty": {
			Rec: ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			},
			Schema: TableSchema{
				Columns: []TableColumn{
					{Name: "entity", ValueType: String},
					{Name: "value", ValueType: Int},
					{Name: "timestamp", ValueType: Timestamp},
				},
			},
			Records:     []GenericRecord{},
			ExpectError: false,
			Expected:    []GenericRecord{},
		},
		"SimpleWrite": {
			Rec: ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			},
			Schema: TableSchema{
				Columns: []TableColumn{
					{Name: "entity", ValueType: String},
					{Name: "value", ValueType: Int},
					{Name: "timestamp", ValueType: Timestamp},
				},
			},
			Records: []GenericRecord{
				[]interface{}{"a", 1, time.UnixMilli(0)},
				[]interface{}{"b", 2, time.UnixMilli(0)},
				[]interface{}{"c", 3, time.UnixMilli(0)},
			},
			ExpectError: false,
			Expected:    []GenericRecord{},
		},
	}

	testTableWrite := func(t *testing.T, test TestCase) {
		table, err := store.CreatePrimaryTable(test.Rec, test.Schema)
		if err != nil {
			t.Fatalf("Could not create table: %v", err)
		}
		_, err = store.GetPrimaryTable(test.Rec) // Need To Fix Schema Here
		if err != nil {
			t.Fatalf("Could not get Primary table: %v", err)
		}
		for _, record := range test.Records {
			if err := table.Write(record); err != nil {
				t.Fatalf("Could not write: %v", err)
			}
		}
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			testTableWrite(t, test)
		})
	}

}

func testTransform(t *testing.T, store OfflineStore) {

	type TransformTest struct {
		PrimaryTable ResourceID
		Schema       TableSchema
		Records      []GenericRecord
		Config       TransformationConfig
		Expected     []GenericRecord
	}

	tests := map[string]TransformTest{
		"Simple": {
			PrimaryTable: ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			},
			Schema: TableSchema{
				Columns: []TableColumn{
					{Name: "entity", ValueType: String},
					{Name: "int", ValueType: Int},
					{Name: "flt", ValueType: Float64},
					{Name: "str", ValueType: String},
					{Name: "bool", ValueType: Bool},
					{Name: "ts", ValueType: Timestamp},
				},
			},
			Records: []GenericRecord{
				[]interface{}{"a", 1, 1.1, "test string", true, time.UnixMilli(0)},
				[]interface{}{"b", 2, 1.2, "second string", false, time.UnixMilli(0)},
				[]interface{}{"c", 3, 1.3, "third string", nil, time.UnixMilli(0)},
				[]interface{}{"d", 4, 1.4, "fourth string", false, time.UnixMilli(0)},
				[]interface{}{"e", 5, 1.5, "fifth string", true, time.UnixMilli(0)},
			},
			Config: TransformationConfig{
				TargetTableID: ResourceID{
					Name: uuid.NewString(),
					Type: Transformation,
				},
				Query: "SELECT * FROM tb",
			},
			Expected: []GenericRecord{
				[]interface{}{"a", 1, 1.1, "test string", true, time.UnixMilli(0).UTC()},
				[]interface{}{"b", 2, 1.2, "second string", false, time.UnixMilli(0).UTC()},
				[]interface{}{"c", 3, 1.3, "third string", nil, time.UnixMilli(0).UTC()},
				[]interface{}{"d", 4, 1.4, "fourth string", false, time.UnixMilli(0).UTC()},
				[]interface{}{"e", 5, 1.5, "fifth string", true, time.UnixMilli(0).UTC()},
			},
		},
		"Count": {
			PrimaryTable: ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			},
			Schema: TableSchema{
				Columns: []TableColumn{
					{Name: "entity", ValueType: String},
					{Name: "int", ValueType: Int},
					{Name: "str", ValueType: String},
					{Name: "bool", ValueType: Bool},
					{Name: "ts", ValueType: Timestamp},
				},
			},
			Records: []GenericRecord{
				[]interface{}{"a", 1, "test string", true, time.UnixMilli(0)},
				[]interface{}{"b", 2, "second string", false, time.UnixMilli(0)},
				[]interface{}{"c", 3, "third string", nil, time.UnixMilli(0)},
				[]interface{}{"d", 4, "fourth string", false, time.UnixMilli(0)},
				[]interface{}{"e", 5, "fifth string", true, time.UnixMilli(0)},
			},
			Config: TransformationConfig{
				TargetTableID: ResourceID{
					Name: uuid.NewString(),
					Type: Transformation,
				},
				Query: "SELECT COUNT(*) FROM tb",
			},
			Expected: []GenericRecord{
				[]interface{}{5},
			},
		},
	}

	testTransform := func(t *testing.T, test TransformTest) {
		table, err := store.CreatePrimaryTable(test.PrimaryTable, test.Schema)
		if err != nil {
			t.Fatalf("Could not initialize table: %v", err)
		}
		for _, value := range test.Records {
			if err := table.Write(value); err != nil {
				t.Fatalf("Could not write value: %v: %v", err, value)
			}
		}
		test.Config.Query = strings.Replace(test.Config.Query, "tb", sanitize(table.GetName()), 1)
		if err := store.CreateTransformation(test.Config); err != nil {
			t.Fatalf("Could not create transformation: %v", err)
		}
		rows, err := table.NumRows()
		if err != nil {
			t.Fatalf("could not get NumRows of table: %v", err)
		}
		if int(rows) != len(test.Records) {
			t.Fatalf("NumRows do not match. Expected: %d, Got: %d", len(test.Records), rows)
		}
		table, err = store.GetTransformationTable(test.Config.TargetTableID)
		if err != nil {
			t.Errorf("Could not get transformation table: %v", err)
		}
		iterator, err := table.IterateSegment(100)
		if err != nil {
			t.Fatalf("Could not get generic iterator: %v", err)
		}
		i := 0
		for iterator.Next() {
			if !reflect.DeepEqual(iterator.Values(), test.Expected[i]) {
				t.Fatalf("Expected: %#v, Received %#v", test.Expected[i], iterator.Values())
			}
			i++
		}

	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			testTransform(t, test)
		})
	}

}

func testTransformCreateFeature(t *testing.T, store OfflineStore) {
	type TransformTest struct {
		PrimaryTable ResourceID
		Schema       TableSchema
		Records      []GenericRecord
		Config       TransformationConfig
		Expected     []GenericRecord
	}

	tests := map[string]TransformTest{
		"Simple": {
			PrimaryTable: ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			},
			Schema: TableSchema{
				Columns: []TableColumn{
					{Name: "entity", ValueType: String},
					{Name: "int", ValueType: Int},
					{Name: "flt", ValueType: Float64},
					{Name: "str", ValueType: String},
					{Name: "bool", ValueType: Bool},
					{Name: "ts", ValueType: Timestamp},
				},
			},
			Records: []GenericRecord{
				[]interface{}{"a", 1, 1.1, "test string", true, time.UnixMilli(0)},
				[]interface{}{"b", 2, 1.2, "second string", false, time.UnixMilli(0)},
				[]interface{}{"c", 3, 1.3, "third string", nil, time.UnixMilli(0)},
				[]interface{}{"d", 4, 1.4, "fourth string", false, time.UnixMilli(0)},
				[]interface{}{"e", 5, 1.5, "fifth string", true, time.UnixMilli(0)},
			},
			Config: TransformationConfig{
				TargetTableID: ResourceID{
					Name: uuid.NewString(),
					Type: Feature,
				},
				Query: "SELECT entity, int, ts FROM tb",
				ColumnMapping: []ColumnMapping{
					{sourceColumn: "entity", resourceColumn: "entity"},
					{sourceColumn: "int", resourceColumn: "value"},
					{sourceColumn: "ts", resourceColumn: "ts"},
				},
			},
			Expected: []GenericRecord{
				[]interface{}{"a", 1, 1.1, "test string", true, time.UnixMilli(0).UTC()},
				[]interface{}{"b", 2, 1.2, "second string", false, time.UnixMilli(0).UTC()},
				[]interface{}{"c", 3, 1.3, "third string", nil, time.UnixMilli(0).UTC()},
				[]interface{}{"d", 4, 1.4, "fourth string", false, time.UnixMilli(0).UTC()},
				[]interface{}{"e", 5, 1.5, "fifth string", true, time.UnixMilli(0).UTC()},
			},
		},
	}

	testTransform := func(t *testing.T, test TransformTest) {
		table, err := store.CreatePrimaryTable(test.PrimaryTable, test.Schema)
		if err != nil {
			t.Fatalf("Could not initialize table: %v", err)
		}
		for _, value := range test.Records {
			if err := table.Write(value); err != nil {
				t.Fatalf("Could not write value: %v: %v", err, value)
			}
		}
		test.Config.Query = strings.Replace(test.Config.Query, "tb", sanitize(table.GetName()), 1)
		if err := store.CreateTransformation(test.Config); err != nil {
			t.Fatalf("Could not create transformation: %v", err)
		}
		rows, err := table.NumRows()
		if err != nil {
			t.Fatalf("could not get NumRows of table: %v", err)
		}
		if int(rows) != len(test.Records) {
			t.Fatalf("NumRows do not match. Expected: %d, Got: %d", len(test.Records), rows)
		}
		_, err = store.GetResourceTable(test.Config.TargetTableID)
		if err != nil {
			t.Errorf("Could not get transformation table: %v", err)
			return
		}
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			testTransform(t, test)
		})
	}
	// Test if can materialized a transformed table
}

func testSQLValidity(t *testing.T, store OfflineStore) {
	config := TransformationConfig{
		TargetTableID: ResourceID{
			Name: "dummyTransformation",
			Type: Transformation,
		},
		Query: "CREATE TABLE test (t INT)",
	}
	if err := store.CreateTransformation(config); err == nil || reflect.TypeOf(err) != reflect.TypeOf(InvalidQueryError{}) {
		t.Fatalf("Successfully created invalid query")
	}
	config = TransformationConfig{
		TargetTableID: ResourceID{
			Name: "dummyTransformation",
			Type: Transformation,
		},
		Query: "INSERT INTO test values (1)",
	}
	if err := store.CreateTransformation(config); err == nil || reflect.TypeOf(err) != reflect.TypeOf(InvalidQueryError{}) {
		t.Fatalf("Successfully created invalid query")
	}
}

func testCreateDuplicatePrimaryTable(t *testing.T, store OfflineStore) {
	table := uuid.NewString()
	rec := ResourceID{
		Name: table,
		Type: Primary,
	}
	schema := TableSchema{
		Columns: []TableColumn{
			{
				Name:      "entity",
				ValueType: Int,
			},
		},
	}
	_, err := store.CreatePrimaryTable(rec, schema)
	if err != nil {
		t.Fatalf("Could not create initial table: %v", err)
	}
	_, err = store.CreatePrimaryTable(rec, schema)
	if err == nil {
		t.Fatalf("Successfully create duplicate tables")
	}
}

func testChainTransform(t *testing.T, store OfflineStore) {

	type TransformTest struct {
		PrimaryTable ResourceID
		Schema       TableSchema
		Records      []GenericRecord
		Config       TransformationConfig
		Expected     []GenericRecord
	}

	firstTransformName := uuid.NewString()
	tests := map[string]TransformTest{
		"First": {
			PrimaryTable: ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			},
			Schema: TableSchema{
				Columns: []TableColumn{
					{Name: "entity", ValueType: String},
					{Name: "int", ValueType: Int},
					{Name: "flt", ValueType: Float64},
					{Name: "str", ValueType: String},
					{Name: "bool", ValueType: Bool},
					{Name: "ts", ValueType: Timestamp},
				},
			},
			Records: []GenericRecord{
				[]interface{}{"a", 1, 1.1, "test string", true, time.UnixMilli(0)},
				[]interface{}{"b", 2, 1.2, "second string", false, time.UnixMilli(0)},
				[]interface{}{"c", 3, 1.3, "third string", nil, time.UnixMilli(0)},
				[]interface{}{"d", 4, 1.4, "fourth string", false, time.UnixMilli(0)},
				[]interface{}{"e", 5, 1.5, "fifth string", true, time.UnixMilli(0)},
			},
			Config: TransformationConfig{
				TargetTableID: ResourceID{
					Name: firstTransformName,
					Type: Transformation,
				},
				Query: "SELECT entity, int, flt, str FROM tb",
			},
			Expected: []GenericRecord{
				[]interface{}{"a", 1, 1.1, "test string"},
				[]interface{}{"b", 2, 1.2, "second string"},
				[]interface{}{"c", 3, 1.3, "third string"},
				[]interface{}{"d", 4, 1.4, "fourth string"},
				[]interface{}{"e", 5, 1.5, "fifth string"},
			},
		},
		"Second": {
			PrimaryTable: ResourceID{
				Name: firstTransformName,
				Type: Primary,
			},
			Schema: TableSchema{
				Columns: []TableColumn{
					{Name: "entity", ValueType: String},
					{Name: "int", ValueType: Int},
					{Name: "str", ValueType: String},
				},
			},
			Config: TransformationConfig{
				TargetTableID: ResourceID{
					Name: uuid.NewString(),
					Type: Transformation,
				},
				Query: "SELECT COUNT(*) FROM tb",
			},
			Expected: []GenericRecord{
				[]interface{}{5},
			},
		},
	}

	table, err := store.CreatePrimaryTable(tests["First"].PrimaryTable, tests["First"].Schema)
	if err != nil {
		t.Fatalf("Could not initialize table: %v", err)
	}
	for _, value := range tests["First"].Records {
		if err := table.Write(value); err != nil {
			t.Fatalf("Could not write value: %v: %v", err, value)
		}
	}
	config := TransformationConfig{
		TargetTableID: ResourceID{
			Name: firstTransformName,
			Type: Transformation,
		},
		Query: fmt.Sprintf("SELECT entity, int, flt, str FROM %s", sanitize(table.GetName())),
	}
	if err := store.CreateTransformation(config); err != nil {
		t.Fatalf("Could not create transformation: %v", err)
	}
	rows, err := table.NumRows()
	if err != nil {
		t.Fatalf("could not get NumRows of table: %v", err)
	}
	if int(rows) != len(tests["First"].Records) {
		t.Fatalf("NumRows do not match. Expected: %d, Got: %d", len(tests["First"].Records), rows)
	}
	table, err = store.GetTransformationTable(tests["First"].Config.TargetTableID)
	if err != nil {
		t.Errorf("Could not get transformation table: %v", err)
	}
	iterator, err := table.IterateSegment(100)
	if err != nil {
		t.Fatalf("Could not get generic iterator: %v", err)
	}
	i := 0
	for iterator.Next() {
		if !reflect.DeepEqual(iterator.Values(), tests["First"].Expected[i]) {
			t.Fatalf("Expected: %#v, Received %#v", tests["First"].Expected[i], iterator.Values())
		}
		i++
	}
	secondTransformName := uuid.NewString()
	config = TransformationConfig{
		TargetTableID: ResourceID{
			Name: secondTransformName,
			Type: Transformation,
		},
		Query: fmt.Sprintf("SELECT Count(*) FROM %s", sanitize(table.GetName())),
	}
	if err := store.CreateTransformation(config); err != nil {
		t.Fatalf("Could not create transformation: %v", err)
	}

	table, err = store.GetTransformationTable(config.TargetTableID)
	if err != nil {
		t.Errorf("Could not get transformation table: %v", err)
	}
	iterator, err = table.IterateSegment(100)
	if err != nil {
		t.Fatalf("Could not get generic iterator: %v", err)
	}
	i = 0
	for iterator.Next() {
		if !reflect.DeepEqual(iterator.Values(), tests["Second"].Expected[i]) {
			t.Fatalf("Expected: %#v, Received %#v", tests["Second"].Expected[i], iterator.Values())
		}
		i++
	}

}

func testTransformToMaterialize(t *testing.T, store OfflineStore) {

	type TransformTest struct {
		PrimaryTable ResourceID
		Schema       TableSchema
		Records      []GenericRecord
		Config       TransformationConfig
		Expected     []GenericRecord
	}

	firstTransformName := uuid.NewString()
	tests := map[string]TransformTest{
		"First": {
			PrimaryTable: ResourceID{
				Name: uuid.NewString(),
				Type: Feature,
			},
			Schema: TableSchema{
				Columns: []TableColumn{
					{Name: "entity", ValueType: String},
					{Name: "value", ValueType: Int},
					{Name: "ts", ValueType: Timestamp},
				},
			},
			Records: []GenericRecord{
				[]interface{}{"a", 1, time.UnixMilli(0)},
				[]interface{}{"b", 2, time.UnixMilli(0)},
				[]interface{}{"c", 3, time.UnixMilli(0)},
				[]interface{}{"d", 4, time.UnixMilli(0)},
				[]interface{}{"e", 5, time.UnixMilli(0)},
			},
			Config: TransformationConfig{
				TargetTableID: ResourceID{
					Name: firstTransformName,
					Type: Transformation,
				},
				Query: "SELECT entity, int, flt, str FROM tb",
			},
			Expected: []GenericRecord{
				[]interface{}{"a", 1, time.UnixMilli(0)},
				[]interface{}{"b", 2, time.UnixMilli(0)},
				[]interface{}{"c", 3, time.UnixMilli(0)},
				[]interface{}{"d", 4, time.UnixMilli(0)},
				[]interface{}{"e", 5, time.UnixMilli(0)},
			},
		},
	}

	table, err := store.CreatePrimaryTable(tests["First"].PrimaryTable, tests["First"].Schema)
	if err != nil {
		t.Fatalf("Could not initialize table: %v", err)
	}
	for _, value := range tests["First"].Records {
		if err := table.Write(value); err != nil {
			t.Fatalf("Could not write value: %v: %v", err, value)
		}
	}
	config := TransformationConfig{
		TargetTableID: ResourceID{
			Name: firstTransformName,
			Type: Transformation,
		},
		Query: fmt.Sprintf("SELECT entity, int, flt, str FROM %s", sanitize(table.GetName())),
	}
	if err := store.CreateTransformation(config); err != nil {
		t.Fatalf("Could not create transformation: %v", err)
	}
	rows, err := table.NumRows()
	if err != nil {
		t.Fatalf("could not get NumRows of table: %v", err)
	}
	if int(rows) != len(tests["First"].Records) {
		t.Fatalf("NumRows do not match. Expected: %d, Got: %d", len(tests["First"].Records), rows)
	}
	mat, err := store.CreateMaterialization(tests["First"].Config.TargetTableID)
	if err != nil {
		t.Fatalf("Could not create materialization: %v", err)
	}
	iterator, err := mat.IterateSegment(0, 10)
	if err != nil {
		t.Fatalf("Could not get iterator: %v", err)
	}
	i := 0
	for iterator.Next() {
		if !reflect.DeepEqual(iterator.Value(), tests["First"].Expected[i]) {
			t.Fatalf("Expected: %#v, Got: %#v", tests["First"].Expected[i], iterator.Value())
		}
		i++
	}
}

func Test_mapColumns(t *testing.T) {
	type mappingItem struct {
		Columns   []ColumnMapping
		Query     string
		Result    string
		ShouldErr bool
	}
	tests := map[string]mappingItem{
		"InvalidNumberOfColumns": {
			Columns: []ColumnMapping{
				{sourceColumn: "source", resourceColumn: Entity},
			},
			Query:     "SELECT * FROM null",
			Result:    fmt.Sprintf("( SELECT %s as entity, %s as value, %s as ts FROM ( %s )t  )", "source", "", "", "SELECT * FROM null"),
			ShouldErr: true,
		},
		"MissingValueColumn": {
			Columns: []ColumnMapping{
				{sourceColumn: "e", resourceColumn: Entity},
				{sourceColumn: "t", resourceColumn: Entity},
				{sourceColumn: "v", resourceColumn: TS},
			},
			Query:     "SELECT * FROM null",
			Result:    fmt.Sprintf("( SELECT %s as entity, %s as value, %s as ts FROM ( %s )t  )", "source", "", "", "SELECT * FROM null"),
			ShouldErr: true,
		},
		"MissingEntityColumn": {
			Columns: []ColumnMapping{
				{sourceColumn: "e", resourceColumn: Value},
				{sourceColumn: "t", resourceColumn: Value},
				{sourceColumn: "v", resourceColumn: TS},
			},
			Query:     "SELECT * FROM null",
			Result:    fmt.Sprintf("( SELECT %s as entity, %s as value, %s as ts FROM ( %s )t  )", "source", "", "", "SELECT * FROM null"),
			ShouldErr: true,
		},
		"MissingTSColumn": {
			Columns: []ColumnMapping{
				{sourceColumn: "e", resourceColumn: Entity},
				{sourceColumn: "t", resourceColumn: Value},
				{sourceColumn: "v", resourceColumn: Value},
			},
			Query:     "SELECT * FROM null",
			Result:    fmt.Sprintf("( SELECT %s as entity, %s as value, %s as ts FROM ( %s )t  )", "source", "", "", "SELECT * FROM null"),
			ShouldErr: true,
		},
		"SimpleSuccess": {
			Columns: []ColumnMapping{
				{sourceColumn: "e", resourceColumn: Entity},
				{sourceColumn: "t", resourceColumn: Value},
				{sourceColumn: "v", resourceColumn: TS},
			},
			Query:     "SELECT * FROM null",
			Result:    fmt.Sprintf("( SELECT %s as entity, %s as value, %s as ts FROM ( %s )t  )", "e", "t", "v", "SELECT * FROM null"),
			ShouldErr: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			query, err := mapColumns(tt.Columns, tt.Query)
			if err != nil && !tt.ShouldErr {
				t.Fatalf("Unexpected Error: %v", err)
			} else if err != nil && tt.ShouldErr {
				return
			} else if tt.Result != query {
				t.Fatalf("Expected: %s\nRecieved: %s", tt.Result, query)
			}
		})
	}

}

func Test_createResourceFromSource(t *testing.T) {
	err := godotenv.Load(".env")
	if err != nil {
		fmt.Println(err)
	}
	var postgresConfig = PostgresConfig{
		Host:     "localhost",
		Port:     "5432",
		Database: os.Getenv("POSTGRES_DB"),
		Username: os.Getenv("POSTGRES_USER"),
		Password: os.Getenv("POSTGRES_PASSWORD"),
	}
	serialPGConfig := postgresConfig.Serialize()
	os.Setenv("TZ", "UTC")
	pgProvider, err := Get(PostgresOffline, serialPGConfig)
	if err != nil {
		t.Fatal("Failed to get postgres provider")
	}
	snowFlakeDatabase := strings.ToUpper(uuid.NewString())
	t.Log("Snowflake Database: ", snowFlakeDatabase)
	var snowflakeConfig = SnowflakeConfig{
		Username:     os.Getenv("SNOWFLAKE_USERNAME"),
		Password:     os.Getenv("SNOWFLAKE_PASSWORD"),
		Organization: os.Getenv("SNOWFLAKE_ORG"),
		Account:      os.Getenv("SNOWFLAKE_ACCOUNT"),
		Database:     snowFlakeDatabase,
	}
	serialSFConfig := snowflakeConfig.Serialize()
	if err := createSnowflakeDatabase(snowflakeConfig); err != nil {
		t.Fatalf("%v", err)
	}
	defer destroySnowflakeDatabase(snowflakeConfig)
	sfProvider, err := Get(SnowflakeOffline, serialSFConfig)
	if err != nil {
		t.Fatal("Failed to get postgres provider")
	}

	for name, provider := range map[string]Provider{"POSTGRES": pgProvider, "SNOWFLAKE": sfProvider} {
		t.Run(name, func(t *testing.T) {

			store, err := provider.AsOfflineStore()
			// Create a generic table to test with
			primaryID := ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			}
			schema := TableSchema{
				Columns: []TableColumn{
					{Name: "col1", ValueType: String},
					{Name: "col2", ValueType: Int},
					{Name: "col3", ValueType: String},
					{Name: "col4", ValueType: Timestamp},
				},
			}
			table, err := store.CreatePrimaryTable(primaryID, schema)
			if err != nil {
				t.Fatalf("Could not create primary table: %v", err)
			}
			records := []GenericRecord{
				{"a", 1, "one", time.UnixMilli(0)},
				{"b", 2, "two", time.UnixMilli(1)},
				{"c", 3, "three", time.UnixMilli(2)},
				{"d", 4, "four", time.UnixMilli(3)},
				{"e", 5, "five", time.UnixMilli(4)},
			}
			for _, record := range records {
				if err := table.Write(record); err != nil {
					t.Fatalf("Could not write record: %v", err)
				}
			}
			featureID := ResourceID{
				Name: uuid.NewString(),
				Type: Feature,
			}
			recSchema := ResourceSchema{
				Entity:      "col1",
				Value:       "col2",
				TS:          "col4",
				SourceTable: table.GetName(),
			}
			t.Log("Resource Name: ", featureID.Name)
			_, err = store.RegisterResourceFromSourceTable(featureID, recSchema)
			if err != nil {
				t.Fatalf("Could not register from Primary Table: %s", err)
			}
			_, err = store.GetResourceTable(featureID)
			if err != nil {
				t.Fatalf("Could not get resource table: %v", err)
			}
		})
	}
}

func Test_createResourceFromSourceNoTS(t *testing.T) {
	err := godotenv.Load(".env")
	if err != nil {
		fmt.Println(err)
	}
	var postgresConfig = PostgresConfig{
		Host:     "localhost",
		Port:     "5432",
		Database: os.Getenv("POSTGRES_DB"),
		Username: os.Getenv("POSTGRES_USER"),
		Password: os.Getenv("POSTGRES_PASSWORD"),
	}
	serialPGConfig := postgresConfig.Serialize()
	os.Setenv("TZ", "UTC")
	pgProvider, err := Get(PostgresOffline, serialPGConfig)
	if err != nil {
		t.Fatal("Failed to get postgres provider")
	}
	snowFlakeDatabase := strings.ToUpper(uuid.NewString())
	t.Log("Snowflake Database: ", snowFlakeDatabase)
	var snowflakeConfig = SnowflakeConfig{
		Username:     os.Getenv("SNOWFLAKE_USERNAME"),
		Password:     os.Getenv("SNOWFLAKE_PASSWORD"),
		Organization: os.Getenv("SNOWFLAKE_ORG"),
		Account:      os.Getenv("SNOWFLAKE_ACCOUNT"),
		Database:     snowFlakeDatabase,
	}
	serialSFConfig := snowflakeConfig.Serialize()
	if err := createSnowflakeDatabase(snowflakeConfig); err != nil {
		t.Fatalf("%v", err)
	}
	defer destroySnowflakeDatabase(snowflakeConfig)
	sfProvider, err := Get(SnowflakeOffline, serialSFConfig)
	if err != nil {
		t.Fatal("Failed to get postgres provider")
	}
	for name, provider := range map[string]Provider{"POSTGRES": pgProvider, "SNOWFLAKE": sfProvider} {
		t.Run(name, func(t *testing.T) {

			store, err := provider.AsOfflineStore()
			// Create a generic table to test with
			primaryID := ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			}
			schema := TableSchema{
				Columns: []TableColumn{
					{Name: "col1", ValueType: String},
					{Name: "col2", ValueType: Int},
					{Name: "col3", ValueType: String},
				},
			}
			table, err := store.CreatePrimaryTable(primaryID, schema)
			if err != nil {
				t.Fatalf("Could not create primary table: %v", err)
			}
			records := []GenericRecord{
				{"a", 1, "one"},
				{"b", 2, "two"},
				{"c", 3, "three"},
				{"d", 4, "four"},
				{"e", 5, "five"},
			}
			for _, record := range records {
				if err := table.Write(record); err != nil {
					t.Fatalf("Could not write record: %v", err)
				}
			}
			featureID := ResourceID{
				Name: uuid.NewString(),
				Type: Feature,
			}
			recSchema := ResourceSchema{
				Entity:      "col1",
				Value:       "col2",
				SourceTable: table.GetName(),
			}
			t.Log("Resource Name: ", featureID.Name)
			_, err = store.RegisterResourceFromSourceTable(featureID, recSchema)
			if err != nil {
				t.Fatalf("Could not register from Source Table: %s", err)
			}
			_, err = store.GetResourceTable(featureID)
			if err != nil {
				t.Fatalf("Could not get resource table: %v", err)
			}
		})
	}
}

func Test_createPrimaryFromSource(t *testing.T) {
	err := godotenv.Load(".env")
	if err != nil {
		fmt.Println(err)
	}
	var postgresConfig = PostgresConfig{
		Host:     "localhost",
		Port:     "5432",
		Database: os.Getenv("POSTGRES_DB"),
		Username: os.Getenv("POSTGRES_USER"),
		Password: os.Getenv("POSTGRES_PASSWORD"),
	}
	serialPGConfig := postgresConfig.Serialize()
	os.Setenv("TZ", "UTC")
	pgProvider, err := Get(PostgresOffline, serialPGConfig)
	if err != nil {
		t.Fatal("Failed to get postgres provider")
	}
	snowFlakeDatabase := strings.ToUpper(uuid.NewString())
	t.Log("Snowflake Database: ", snowFlakeDatabase)
	var snowflakeConfig = SnowflakeConfig{
		Username:     os.Getenv("SNOWFLAKE_USERNAME"),
		Password:     os.Getenv("SNOWFLAKE_PASSWORD"),
		Organization: os.Getenv("SNOWFLAKE_ORG"),
		Account:      os.Getenv("SNOWFLAKE_ACCOUNT"),
		Database:     snowFlakeDatabase,
	}
	serialSFConfig := snowflakeConfig.Serialize()
	if err := createSnowflakeDatabase(snowflakeConfig); err != nil {
		t.Fatalf("%v", err)
	}
	defer destroySnowflakeDatabase(snowflakeConfig)
	sfProvider, err := Get(SnowflakeOffline, serialSFConfig)
	if err != nil {
		t.Fatal("Failed to get postgres provider")
	}
	for name, provider := range map[string]Provider{"POSTGRES": pgProvider, "SNOWFLAKE": sfProvider} {
		t.Run(name, func(t *testing.T) {
			store, err := provider.AsOfflineStore()
			// Create a generic table to test with
			primaryID := ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			}
			schema := TableSchema{
				Columns: []TableColumn{
					{Name: "col1", ValueType: String},
					{Name: "col2", ValueType: Int},
					{Name: "col3", ValueType: String},
					{Name: "col4", ValueType: Timestamp},
				},
			}
			table, err := store.CreatePrimaryTable(primaryID, schema)
			if err != nil {
				t.Fatalf("Could not create primary table: %v", err)
			}
			records := []GenericRecord{
				{"a", 1, "one", time.UnixMilli(0)},
				{"b", 2, "two", time.UnixMilli(1)},
				{"c", 3, "three", time.UnixMilli(2)},
				{"d", 4, "four", time.UnixMilli(3)},
				{"e", 5, "five", time.UnixMilli(4)},
			}
			for _, record := range records {
				if err := table.Write(record); err != nil {
					t.Fatalf("Could not write record: %v", err)
				}
			}
			primaryCopyID := ResourceID{
				Name: uuid.NewString(),
				Type: Primary,
			}

			t.Log("Primary Name: ", primaryCopyID.Name)
			_, err = store.RegisterPrimaryFromSourceTable(primaryCopyID, table.GetName())
			if err != nil {
				t.Fatalf("Could not register from Source Table: %s", err)
			}
			_, err = store.GetPrimaryTable(primaryCopyID)
			if err != nil {
				t.Fatalf("Could not get primary table: %v", err)
			}
		})
	}
}

func Test_snowflakeOfflineTable_checkTimestamp(t *testing.T) {
	type fields struct {
		db   *sql.DB
		name string
	}
	type args struct {
		rec ResourceRecord
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   ResourceRecord
	}{
		{"Nil TimeStamp", fields{nil, ""}, args{rec: ResourceRecord{}}, ResourceRecord{TS: time.UnixMilli(0).UTC()}},
		{"Non Nil TimeStamp", fields{nil, ""}, args{rec: ResourceRecord{TS: time.UnixMilli(10)}}, ResourceRecord{TS: time.UnixMilli(10)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkTimestamp(tt.args.rec); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("checkTimestamp() = %v, want %v", got, tt.want)
			}
		})
	}
}
