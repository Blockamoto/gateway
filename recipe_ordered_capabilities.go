package main

func recipeOrderedSteps() []string {
	return []string{"for_events", "for_range", "let", "read", "put", "delete", "if", "require", "emit"}
}
func recipeOrderedEvents() []string {
	return []string{"tx_begin", "input", "outputs_begin", "output", "tx_end"}
}
func recipeOrderedFunctions() []recipeFunctionCapability {
	arg := func(name string, types ...string) recipeArgumentCapability {
		return recipeArgumentCapability{Name: name, Types: types}
	}
	function := func(name, result string, args ...recipeArgumentCapability) recipeFunctionCapability {
		return recipeFunctionCapability{Name: name, Arguments: args, Result: recipeTypeSpec{Types: []string{result}}}
	}
	return []recipeFunctionCapability{
		function("eq", "bool", arg("left", "text", "uint", "bool"), arg("right", "text", "uint", "bool")),
		function("not", "bool", arg("value", "bool")), function("present", "bool", arg("value", "any")),
		function("add_money", "uint", arg("left", "uint"), arg("right", "uint")), function("sub_money", "uint", arg("left", "uint"), arg("right", "uint")),
		function("less_equal", "bool", arg("left", "uint"), arg("right", "uint")),
		function("outpoint", "text", arg("txid", "text"), arg("vout", "uint")),
		function("output_ref", "text", arg("height", "uint"), arg("tx_index", "uint"), arg("vout", "uint")),
		function("destination", "object", arg("script", "bytes")), function("retains", "bool", arg("height", "uint"), arg("script", "bytes")),
		function("payment_destination", "object", arg("script", "bytes")),
		function("bip30_exception", "bool", arg("height", "uint"), arg("block", "text"), arg("coinbase", "bool")),
		function("get", "any", arg("object", "object"), arg("key", "text")),
		function("range_empty", "ranges"), function("range_subsidy", "ranges", arg("height", "uint")),
		function("range_concat", "ranges", arg("left", "ranges"), arg("right", "ranges")),
		function("range_split", "object", arg("ranges", "ranges"), arg("value", "uint")),
		function("range_value", "uint", arg("ranges", "ranges")), function("range_at", "uint", arg("ranges", "ranges"), arg("offset", "uint")),
	}
}
