package driver

import "testing"

func TestDBNameFromURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		want    string
		wantErr bool
	}{
		{name: "host and db", uri: "mongodb://localhost:27017/mydb", want: "mydb"},
		{name: "trailing slash", uri: "mongodb://localhost:27017/mydb/", want: "mydb"},
		{name: "with options", uri: "mongodb://localhost:27017/mydb?retryWrites=true", want: "mydb"},
		{name: "credentials", uri: "mongodb://user:pass@localhost:27017/mydb", want: "mydb"},
		{name: "replica set", uri: "mongodb://h1:27017,h2:27017/mydb?replicaSet=rs0", want: "mydb"},
		{name: "srv scheme", uri: "mongodb+srv://cluster.example.com/mydb", want: "mydb"},
		{name: "no db", uri: "mongodb://localhost:27017", wantErr: true},
		{name: "no db trailing slash", uri: "mongodb://localhost:27017/", wantErr: true},
		{name: "no db with options", uri: "mongodb://localhost:27017/?retryWrites=true", wantErr: true},
		{name: "unparseable", uri: "://nonsense", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DBNameFromURI(tt.uri)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("DBNameFromURI(%q) = %q, want error", tt.uri, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DBNameFromURI(%q) unexpected error: %v", tt.uri, err)
			}
			if got != tt.want {
				t.Errorf("DBNameFromURI(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}
