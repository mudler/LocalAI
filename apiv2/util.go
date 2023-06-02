package apiv2

import (
	"fmt"
	"reflect"
	"runtime"
	// "github.com/mitchellh/mapstructure"
)

// Not sure if there's a better place for this, so stuff it in a utils.go for now

// This function grabs the name of the function that calls it, skipping up the callstack `skip` levels.
// This is probably a go war crime, but NJ method and all. It's an awesome way to index EndpointConfigMap
func printCurrentFunctionName(skip int) string {
	pc, _, _, _ := runtime.Caller(skip)
	funcName := runtime.FuncForPC(pc).Name()
	fmt.Println("Current function:", funcName)
	return funcName
}

// This is another dubious one - Decode hooks are hard.... so this function just takes a perfectly good struct and copies all the nonzero fields out to a map[string]interface{} which mapstructure handles correctly already :)
func structToStrippedMap(s interface{}) map[string]interface{} {
	m := make(map[string]interface{})

	// Get the reflect.Value of the struct
	v := reflect.ValueOf(s)

	// Get the reflect.Type of the struct
	t := v.Type()

	// Iterate over each field of the struct
	for i := 0; i < v.NumField(); i++ {
		// Get the field's reflect.Value
		fieldValue := v.Field(i)

		// Get the field's reflect.StructField
		field := t.Field(i)

		// Skip unexported fields and zero values
		if !fieldValue.CanInterface() || fieldValue.IsZero() {
			continue
		}

		// Add the field name and value to the map
		m[field.Name] = fieldValue.Interface()
	}

	return m
}

// func NilToEmptyHook(from reflect.Type, to reflect.Type, data interface{}) (interface{}, error) {
// 	fmt.Printf("* to.Kind %+v\ndata: %+v", to.Kind(), data)
// 	if to.Kind() == reflect.Ptr && reflect.ValueOf(data).IsNil() {
// 		fmt.Println("!!!!!HIT")
// 		return reflect.Zero(to).Interface(), nil
// 	}
// 	return data, nil
// }

// func GetConfigMergingDecoderConfig(result interface{}) mapstructure.DecoderConfig {

// 	return mapstructure.DecoderConfig{
// 		Result:     result,
// 		DecodeHook: mapstructure.ComposeDecodeHookFunc(NilToEmptyHook),
// 	}
// }
