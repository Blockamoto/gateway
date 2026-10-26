// Command index-demo runs the experiment on a local JSON fixture, not a node.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	engine "gateway-index-experiment"
)

type fixture struct {
	Block     engine.Block                   `json:"block"`
	Locations map[string]engine.LookupResult `json:"locations"`
}
type resolver map[string]engine.LookupResult

func (r resolver) ResolveTransaction(id string) (engine.LookupResult, bool, error) {
	v, ok := r[id]
	return v, ok, nil
}

func main() {
	definition := flag.String("definition", "definitions/spends.json", "JSON executable definition")
	input := flag.String("fixture", "fixtures/block.json", "JSON test block and lookup results")
	flag.Parse()
	if err := run(*definition, *input); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(path, input string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d, err := engine.DecodeDefinition(f)
	if err != nil {
		return err
	}
	b, err := os.Open(input)
	if err != nil {
		return err
	}
	defer b.Close()
	var data fixture
	decoder := json.NewDecoder(b)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return err
	}
	result, err := engine.Run(d, data.Block, resolver(data.Locations))
	if err != nil {
		return err
	}
	output := json.NewEncoder(os.Stdout)
	output.SetIndent("", "  ")
	return output.Encode(result)
}
