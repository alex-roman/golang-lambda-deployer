package pkg

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    DeployConfig
		wantErr bool
	}{
		{
			name:    "test",
			payload: "ENV=test\nAPP_NAME=test\nBUILDS_BUCKET=test\nLOG_GROUP_NAME=test",
			want: DeployConfig{
				Env:          "test",
				AppName:      "test",
				BuildsBucket: "test",
				LogGroupName: "test",
			},
		},
		{
			name:    "test with defaults",
			payload: "APP_NAME=test",
			want: DeployConfig{
				AppName: "test",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := os.WriteFile("deploy.conf", []byte(tt.payload), 0644)
			require.NoError(t, err)
			defer os.Remove("deploy.conf")

			got, err := LoadConfig()
			if tt.wantErr {
				require.Error(t, err)
			}
			require.Equal(t, tt.want, got)
		})
	}
}
