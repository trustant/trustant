// Copyright 2025-2026 Nuvolaris Inc
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"encoding/json"
	"testing"
)

func TestResolveStatus(t *testing.T) {
	decode := func(t *testing.T, body []byte) map[string]json.RawMessage {
		t.Helper()
		var m map[string]json.RawMessage
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		return m
	}
	const file = `{"messages":{"v1.0beta":"current","default":"welcome"},"ollama":{"modelsVersion":4},"trustant":{"modelsVersion":3}}`

	for _, tc := range []struct {
		name, body, version, want string
	}{
		{"exact version", file, "v1.0beta", `"current"`},
		{"default fallback", file, "v9.9", `"welcome"`},
		{"no default", `{"messages":{"v1.0beta":"current"}}`, "v9.9", `""`},
		{"no messages", `{"ollama":{}}`, "v1.0beta", `""`},
		{"malformed messages", `{"messages":[1]}`, "v1.0beta", `""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := resolveStatus([]byte(tc.body), tc.version)
			if err != nil {
				t.Fatal(err)
			}
			got := decode(t, out)
			if string(got["message"]) != tc.want {
				t.Fatalf("message = %s, want %s", got["message"], tc.want)
			}
			if _, ok := got["messages"]; ok {
				t.Fatal("messages map was passed through")
			}
		})
	}

	t.Run("provider blocks pass through", func(t *testing.T) {
		out, err := resolveStatus([]byte(file), "v1.0beta")
		if err != nil {
			t.Fatal(err)
		}
		got := decode(t, out)
		if string(got["ollama"]) != `{"modelsVersion":4}` || string(got["trustant"]) != `{"modelsVersion":3}` {
			t.Fatalf("providers changed: ollama=%s trustant=%s", got["ollama"], got["trustant"])
		}
	})

	t.Run("rejects a non-object body", func(t *testing.T) {
		for _, in := range []string{`[1,2]`, `not json`, `null`} {
			if _, err := resolveStatus([]byte(in), "v1"); err == nil {
				t.Fatalf("body %q accepted", in)
			}
		}
	})
}
