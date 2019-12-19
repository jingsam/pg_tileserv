package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	// "net/url"

	// REST routing

	// Database
	"context"
	// "github.com/jackc/pgtype"
	// "github.com/lib/pq"
	// "github.com/jackc/pgtype"

	// Logging
	log "github.com/sirupsen/logrus"

	// Configuration
	"github.com/spf13/viper"
)

// type LayerTable struct {
type LayerFunction struct {
	Id          string
	Schema      string
	Function    string
	Description string
	Arguments   map[string]FunctionArgument
	MinZoom     int
	MaxZoom     int
	Tiles       string
	SourceLayer string
}

type FunctionArgument struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default string `json:"default,omitempty"`
	order   int
}

type FunctionDetailJson struct {
	Id          string             `json:"id"`
	Schema      string             `json:"schema"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Arguments   []FunctionArgument `json:"arguments,omitempty"`
	MinZoom     int                `json:"minzoom"`
	MaxZoom     int                `json:"maxzoom"`
	TileUrl     string             `json:"tileurl"`
	SourceLayer string             `json:"sourcelayer"`
}

/********************************************************************************
 * Layer Interface
 */

func (lyr LayerFunction) GetType() layerType {
	return layerTypeFunction
}

func (lyr LayerFunction) GetId() string {
	return lyr.Id
}

func (lyr LayerFunction) GetDescription() string {
	return lyr.Description
}

func (lyr LayerFunction) GetName() string {
	return lyr.Function
}

func (lyr LayerFunction) GetSchema() string {
	return lyr.Schema
}

func (lyr LayerFunction) GetTileRequest(tile Tile, r *http.Request) TileRequest {

	procArgs := lyr.getFunctionArgs(r.URL.Query())
	sql, data, _ := lyr.requestSql(tile, procArgs)

	tr := TileRequest{
		Tile: tile,
		Sql:  sql,
		Args: data,
	}
	return tr
}

func (lyr LayerFunction) WriteLayerJson(w http.ResponseWriter, req *http.Request) error {
	jsonTableDetail, err := lyr.getFunctionDetailJson(req)
	if err != nil {
		return err
	}
	w.Header().Add("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonTableDetail)
	// all good, no error
	return nil
}

/********************************************************************************/

func (lyr *LayerFunction) requestSql(tile Tile, args map[string]string) (string, []interface{}, error) {
	return "", make([]interface{}, 0), nil
}

func (lyr *LayerFunction) getFunctionArgs(vals url.Values) map[string]string {
	funcArgs := make(map[string]string)
	for k, v := range vals {
		if arg, ok := lyr.Arguments[k]; ok {
			funcArgs[arg.Name] = v[0]
		}
	}
	return funcArgs
}

func (lyr *LayerFunction) getFunctionDetailJson(req *http.Request) (FunctionDetailJson, error) {

	td := FunctionDetailJson{
		Id:          lyr.Id,
		Schema:      lyr.Schema,
		Name:        lyr.Function,
		Description: lyr.Description,
		MinZoom:     viper.GetInt("DefaultMinZoom"),
		MaxZoom:     viper.GetInt("DefaultMaxZoom"),
		SourceLayer: lyr.Id,
	}
	// TileURL is relative to server base
	td.TileUrl = fmt.Sprintf("%s/%s/{z}/{x}/{y}.pbf", serverURLBase(req), lyr.Id)

	// Want to add the attributes to the Json representation
	// in table order, which is fiddly
	tmpMap := make(map[int]FunctionArgument)
	tmpKeys := make([]int, 0, len(lyr.Arguments))
	for _, v := range lyr.Arguments {
		tmpMap[v.order] = v
		tmpKeys = append(tmpKeys, v.order)
	}
	sort.Ints(tmpKeys)
	for _, v := range tmpKeys {
		td.Arguments = append(td.Arguments, tmpMap[v])
	}
	return td, nil
}

/*
func (lyr *LayerFunction) GetLayerFunctionArgs(vals url.Values) map[string]string {
	funcArgs := make(map[string]string)
	for _, arg := range lyr.Arguments {
		if val, ok := vals[arg]; ok {
			funcArgs[arg] = val[0]
		}
	}
	return funcArgs
}
*/

func (lyr *LayerFunction) GetTile(tile *Tile, args map[string]string) ([]byte, error) {

	db, err := DbConnect()
	if err != nil {
		log.Fatal(err)
	}

	// Need ordered list of named parameters and values to
	// pass into the Query
	keys := make([]string, 0)
	vals := make([]interface{}, 0)
	i := 1
	for k, v := range args {
		keys = append(keys, fmt.Sprintf("%s => $%d", k, i))
		switch k {
		case "x":
			vals = append(vals, tile.X)
		case "y":
			vals = append(vals, tile.Y)
		case "z":
			vals = append(vals, tile.Zoom)
		default:
			vals = append(vals, v)
		}
		i += 1
	}

	// Build the SQL
	sql := fmt.Sprintf("SELECT %s(%s)", lyr.Id, strings.Join(keys, ", "))
	log.WithFields(log.Fields{
		"event": "function.gettile",
		"topic": "sql",
		"key":   sql,
	}).Debugf("Func GetTile: %s", sql)

	row := db.QueryRow(context.Background(), sql, vals...)
	var mvtTile []byte
	err = row.Scan(&mvtTile)
	if err != nil {
		log.Warn(err)
		return nil, err
	} else {
		return mvtTile, nil
	}
}

func GetFunctionLayers() ([]LayerFunction, error) {

	// Valid functions **must** have signature of
	// function(z integer, x integer, y integer) returns bytea
	layerSql := `
		SELECT
		Format('%s.%s', n.nspname, p.proname) AS id,
		n.nspname,
		p.proname,
		coalesce(d.description, '') AS description,
		coalesce(p.proargnames, ARRAY[]::text[]) AS argnames,
		coalesce(string_to_array(oidvectortypes(p.proargtypes),', '), ARRAY[]::text[]) AS argtypes
		FROM pg_proc p
		JOIN pg_namespace n ON (p.pronamespace = n.oid)
		LEFT JOIN pg_description d ON (p.oid = d.objoid)
		WHERE p.proargtypes[0:2] = ARRAY[23::oid, 23::oid, 23::oid]
		AND p.proargnames[1:3] = ARRAY['z'::text, 'x'::text, 'y'::text]
		AND prorettype = 17
		AND has_function_privilege(Format('%s.%s(%s)', n.nspname, p.proname, oidvectortypes(proargtypes)), 'execute') ;
		`

	db, connerr := DbConnect()
	if connerr != nil {
		return nil, connerr
	}

	rows, err := db.Query(context.Background(), layerSql)
	if err != nil {
		return nil, err
	}

	// Reset array of layers
	layerFunctions := make([]LayerFunction, 0)
	for rows.Next() {

		var (
			id, schema, function, description string
			argnames, argtypes                []string
		)

		err := rows.Scan(&id, &schema, &function, &description, &argnames, &argtypes)
		if err != nil {
			log.Fatal(err)
		}

		args := make(map[string]FunctionArgument)
		// First three arguments have to be z, x, y
		for i := 3; i < len(argnames); i++ {
			args[argnames[i]] = FunctionArgument{
				order:   i - 3,
				Name:    argnames[i],
				Type:    argtypes[i],
				Default: "", // TODO, add this in
			}
		}

		lyr := LayerFunction{
			Id:          id,
			Schema:      schema,
			Function:    function,
			Description: description,
			Arguments:   args,
		}

		layerFunctions = append(layerFunctions, lyr)
	}
	// Check for errors from iterating over rows.
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return layerFunctions, nil
}