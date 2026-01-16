package jobs_test

import (
	"context"
	"testing"

	"github.com/databricks/terraform-provider-databricks/internal/acceptance"
	"github.com/databricks/terraform-provider-databricks/internal/providers"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var sdkV2FallbackFactory = map[string]func() (tfprotov6.ProviderServer, error){
	"databricks": func() (tfprotov6.ProviderServer, error) {
		sdkv2Provider, pluginfwProvider := acceptance.ProvidersWithResourceFallbacks([]string{"databricks_job"})
		return providers.GetProviderServer(context.Background(), providers.WithSdkV2Provider(sdkv2Provider), providers.WithPluginFrameworkProvider(pluginfwProvider))
	},
}

func TestAccJobResource(t *testing.T) {
	acceptance.WorkspaceLevel(t, acceptance.Step{
		Template: `
			resource "databricks_job" "this" {
				name = "tf-test-job-{var.RANDOM}"
				task {
					task_key = "task1"
					spark_python_task {
						python_file = "dbfs:/test.py"
					}
					new_cluster {
						num_workers   = 1
						spark_version = "{env.SPARK_VERSION}"
						node_type_id  = "{env.TEST_NODE_TYPE}"
					}
				}
			}
		`,
	})
}

func TestAccJobResourceUpdate(t *testing.T) {
	acceptance.WorkspaceLevel(t,
		acceptance.Step{
			Template: `
				resource "databricks_job" "this" {
					name = "tf-test-job-{var.STICKY_RANDOM}"
					task {
						task_key = "task1"
						spark_python_task {
							python_file = "dbfs:/test.py"
						}
						new_cluster {
							num_workers   = 1
							spark_version = "{env.SPARK_VERSION}"
							node_type_id  = "{env.TEST_NODE_TYPE}"
						}
					}
				}
			`,
		},
		acceptance.Step{
			Template: `
				resource "databricks_job" "this" {
					name = "tf-test-job-{var.STICKY_RANDOM}-updated"
					task {
						task_key = "task1"
						spark_python_task {
							python_file = "dbfs:/test.py"
						}
						new_cluster {
							num_workers   = 2
							spark_version = "{env.SPARK_VERSION}"
							node_type_id  = "{env.TEST_NODE_TYPE}"
						}
					}
				}
			`,
		},
	)
}

// TestAccJobMigrationFromSDKv2 tests the transition from sdkv2 to plugin framework.
// This test verifies that existing state created by SDK v2 implementation can be
// successfully managed by the plugin framework implementation without any changes.
func TestAccJobMigrationFromSDKv2(t *testing.T) {
	acceptance.WorkspaceLevel(t,
		// Step 1: Create job using SDK v2 implementation
		acceptance.Step{
			ProtoV6ProviderFactories: sdkV2FallbackFactory,
			Template: `
				resource "databricks_job" "this" {
					name = "tf-test-job-migration-{var.STICKY_RANDOM}"
					task {
						task_key = "task1"
						spark_python_task {
							python_file = "dbfs:/test.py"
						}
						new_cluster {
							num_workers   = 1
							spark_version = "{env.SPARK_VERSION}"
							node_type_id  = "{env.TEST_NODE_TYPE}"
						}
					}
				}
			`,
		},
		// Step 2: Update the job using plugin framework implementation (default)
		// This verifies no changes are needed when switching implementations
		acceptance.Step{
			Template: `
				resource "databricks_job" "this" {
					name = "tf-test-job-migration-{var.STICKY_RANDOM}-updated"
					task {
						task_key = "task1"
						spark_python_task {
							python_file = "dbfs:/test.py"
						}
						new_cluster {
							num_workers   = 2
							spark_version = "{env.SPARK_VERSION}"
							node_type_id  = "{env.TEST_NODE_TYPE}"
						}
					}
				}
			`,
		},
	)
}

// TestAccJobMigrationFromPluginFramework tests the transition from plugin framework to sdkv2.
// This test verifies that existing state created by plugin framework implementation can be
// successfully managed by the SDK v2 implementation without any changes.
func TestAccJobMigrationFromPluginFramework(t *testing.T) {
	acceptance.WorkspaceLevel(t,
		// Step 1: Create job using plugin framework implementation
		acceptance.Step{
			Template: `
				resource "databricks_job" "this" {
					name = "tf-test-job-migration-rollback-{var.STICKY_RANDOM}"
					task {
						task_key = "task1"
						spark_python_task {
							python_file = "dbfs:/test.py"
						}
						new_cluster {
							num_workers   = 1
							spark_version = "{env.SPARK_VERSION}"
							node_type_id  = "{env.TEST_NODE_TYPE}"
						}
					}
				}
			`,
		},
		// Step 2: Update the job using SDK v2 implementation
		// This verifies no changes are needed when switching implementations
		acceptance.Step{
			ProtoV6ProviderFactories: sdkV2FallbackFactory,
			Template: `
				resource "databricks_job" "this" {
					name = "tf-test-job-migration-rollback-{var.STICKY_RANDOM}"
					task {
						task_key = "task1"
						spark_python_task {
							python_file = "dbfs:/test.py"
						}
						new_cluster {
							num_workers   = 1
							spark_version = "{env.SPARK_VERSION}"
							node_type_id  = "{env.TEST_NODE_TYPE}"
						}
					}
				}
			`,
		},
	)
}
