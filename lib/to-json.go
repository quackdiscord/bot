package lib

import (
	"encoding/json"
)

func ToJSONByteArr(v interface{}) ([]byte, error) {
	jsonData, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonData, nil
}
