package decode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

func JSON(r *http.Request, dest any) error {
	if r.Body == nil {
		return errors.New("empty request body")
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dest); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("empty request body")
		}
		return fmt.Errorf("invalid JSON: %w", err)
	}

	if dec.More() {
		return errors.New("request body must contain a single JSON object")
	}

	return nil
}
