package bmtc

import (
	"encoding/json"
	"fmt"
)

// decodeJSON decodes a non-enveloped body, wrapping errors as ErrShape.
func decodeJSON(data []byte, out any) error {
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: %v", ErrShape, err)
	}
	return nil
}
