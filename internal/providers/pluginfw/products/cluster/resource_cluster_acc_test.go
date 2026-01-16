package cluster_test

import (
	"context"
	"testing"

	"github.com/databricks/terraform-provider-databricks/internal/acceptance"
	"github.com/databricks/terraform-provider-databricks/internal/providers"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var sdkV2FallbackFactory = map[string]func() (tfprotov6.ProviderServer, error){
	"databricks": func() (tfprotov6.ProviderServer, error) {
		sdkv2Provider, pluginfwProvider := acceptance.ProvidersWithResourceFallbacks([]string{"databricks_cluster"})
		return providers.GetProviderServer(context.Background(), providers.WithSdkV2Provider(sdkv2Provider), providers.WithPluginFrameworkProvider(pluginfwProvider))
	},
}

func singleNodeClusterTemplate(autoTerminationMinutes string, isPinned bool) string {
	pinned := "false"
	if isPinned {
		pinned = "true"
	}
	return `
		data "databricks_spark_version" "latest" {
		}
		resource "databricks_cluster" "this" {
			cluster_name = "migration-{var.STICKY_RANDOM}"
			spark_version = data.databricks_spark_version.latest.id
			instance_pool_id = "{env.TEST_INSTANCE_POOL_ID}"
			num_workers = 0
			autotermination_minutes = ` + autoTerminationMinutes + `
			is_pinned = ` + pinned + `
			spark_conf = {
				"spark.databricks.cluster.profile" = "singleNode"
				"spark.master" = "local[*]"
			}
			custom_tags = {
				"ResourceClass" = "SingleNode"
			}
		}
	`
}

func TestAccClusterMigrationFromSDKv2(t *testing.T) {
	acceptance.WorkspaceLevel(t,
		acceptance.Step{
			ProtoV6ProviderFactories: sdkV2FallbackFactory,
			Template:                 singleNodeClusterTemplate("10", false),
		},
		acceptance.Step{
			Template: singleNodeClusterTemplate("20", false),
		},
	)
}

func TestAccClusterMigrationFromPluginFramework(t *testing.T) {
	acceptance.WorkspaceLevel(t,
		acceptance.Step{
			Template: singleNodeClusterTemplate("10", false),
		},
		acceptance.Step{
			ProtoV6ProviderFactories: sdkV2FallbackFactory,
			Template:                 singleNodeClusterTemplate("20", false),
		},
	)
}

func TestAccClusterMigrationWithPinning(t *testing.T) {
	acceptance.WorkspaceLevel(t,
		acceptance.Step{
			ProtoV6ProviderFactories: sdkV2FallbackFactory,
			Template:                 singleNodeClusterTemplate("10", true),
		},
		acceptance.Step{
			Template: singleNodeClusterTemplate("10", false),
		},
	)
}
