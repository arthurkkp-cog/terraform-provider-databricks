# Migration Plan: `databricks_current_user` Data Source from SDKv2 to Plugin Framework

## 1. Overview

This document outlines the migration plan for the `databricks_current_user` data source from Terraform's SDKv2 to the Plugin Framework.

### Data Source Selection Rationale

The `databricks_current_user` data source was selected for this migration plan because:
- It is a relatively simple data source with a straightforward schema (7 computed string fields)
- It has no complex nested structures or lists
- It has existing unit tests that can be adapted
- It provides a good learning opportunity for the migration process before tackling more complex data sources

## 2. Current SDKv2 Implementation

### Location
- **File**: `scim/data_current_user.go`
- **Test File**: `scim/data_current_user_test.go`
- **Registration**: `internal/providers/sdkv2/sdkv2.go` (line 126)

### Current Schema

```go
Schema: map[string]*schema.Schema{
    "user_name": {
        Type:     schema.TypeString,
        Computed: true,
    },
    "home": {
        Type:     schema.TypeString,
        Computed: true,
    },
    "repos": {
        Type:     schema.TypeString,
        Computed: true,
    },
    "alphanumeric": {
        Type:     schema.TypeString,
        Computed: true,
    },
    "external_id": {
        Type:     schema.TypeString,
        Computed: true,
    },
    "workspace_url": {
        Type:     schema.TypeString,
        Computed: true,
    },
    "acl_principal_id": {
        Type:     schema.TypeString,
        Computed: true,
    },
}
```

### Current Read Logic

The data source:
1. Gets the workspace client
2. Calls `w.CurrentUser.Me(ctx)` to fetch current user info
3. Sets computed fields:
   - `user_name`: Direct from API response
   - `home`: Formatted as `/Users/{user_name}`
   - `repos`: Formatted as `/Repos/{user_name}`
   - `acl_principal_id`: `servicePrincipals/{user_name}` if UUID, else `users/{user_name}`
   - `external_id`: Direct from API response
   - `alphanumeric`: Lowercase, non-alphanumeric replaced with `_`, only first part before `@`
   - `workspace_url`: From client config
4. Sets the ID to the user's ID from the API response

## 3. Proposed Plugin Framework Implementation

### Location
- **Directory**: `internal/providers/pluginfw/products/currentuser/`
- **Main File**: `internal/providers/pluginfw/products/currentuser/data_current_user.go`
- **Unit Test File**: `internal/providers/pluginfw/products/currentuser/data_current_user_test.go`
- **Acceptance Test File**: `internal/providers/pluginfw/products/currentuser/data_current_user_acc_test.go`

### Proposed Data Structure

```go
package currentuser

import (
    "context"
    "fmt"
    "reflect"
    "regexp"
    "strings"

    "github.com/databricks/terraform-provider-databricks/common"
    pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
    pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
    "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
    "github.com/hashicorp/terraform-plugin-framework/datasource"
    "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
    "github.com/hashicorp/terraform-plugin-framework/types"
)

const dataSourceName = "current_user"

var nonAlphanumeric = regexp.MustCompile(`\W`)

func DataSourceCurrentUser() datasource.DataSource {
    return &CurrentUserDataSource{}
}

var _ datasource.DataSourceWithConfigure = &CurrentUserDataSource{}

type CurrentUserDataSource struct {
    Client *common.DatabricksClient
}

type CurrentUserData struct {
    ID             types.String `tfsdk:"id"`
    UserName       types.String `tfsdk:"user_name"`
    Home           types.String `tfsdk:"home"`
    Repos          types.String `tfsdk:"repos"`
    Alphanumeric   types.String `tfsdk:"alphanumeric"`
    ExternalId     types.String `tfsdk:"external_id"`
    WorkspaceUrl   types.String `tfsdk:"workspace_url"`
    AclPrincipalId types.String `tfsdk:"acl_principal_id"`
}

func (CurrentUserData) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
    attrs["id"] = attrs["id"].SetComputed()
    attrs["user_name"] = attrs["user_name"].SetComputed()
    attrs["home"] = attrs["home"].SetComputed()
    attrs["repos"] = attrs["repos"].SetComputed()
    attrs["alphanumeric"] = attrs["alphanumeric"].SetComputed()
    attrs["external_id"] = attrs["external_id"].SetComputed()
    attrs["workspace_url"] = attrs["workspace_url"].SetComputed()
    attrs["acl_principal_id"] = attrs["acl_principal_id"].SetComputed()
    return attrs
}

func (CurrentUserData) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
    return map[string]reflect.Type{}
}
```

### Proposed Methods

```go
func (d *CurrentUserDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
    resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceName)
}

func (d *CurrentUserDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
    attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, CurrentUserData{}, nil)
    resp.Schema = schema.Schema{
        Attributes: attrs,
        Blocks:     blocks,
    }
}

func (d *CurrentUserDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
    if d.Client == nil {
        d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
    }
}

func (d *CurrentUserDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
    ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceName)

    w, diags := d.Client.GetWorkspaceClient()
    resp.Diagnostics.Append(diags...)
    if resp.Diagnostics.HasError() {
        return
    }

    me, err := w.CurrentUser.Me(ctx)
    if err != nil {
        resp.Diagnostics.AddError("Failed to fetch current user", err.Error())
        return
    }

    var aclPrincipalId string
    if common.StringIsUUID(me.UserName) {
        aclPrincipalId = fmt.Sprintf("servicePrincipals/%s", me.UserName)
    } else {
        aclPrincipalId = fmt.Sprintf("users/%s", me.UserName)
    }

    splits := strings.Split(me.UserName, "@")
    norm := nonAlphanumeric.ReplaceAllLiteralString(splits[0], "_")
    norm = strings.ToLower(norm)

    currentUserData := CurrentUserData{
        ID:             types.StringValue(me.Id),
        UserName:       types.StringValue(me.UserName),
        Home:           types.StringValue(fmt.Sprintf("/Users/%s", me.UserName)),
        Repos:          types.StringValue(fmt.Sprintf("/Repos/%s", me.UserName)),
        Alphanumeric:   types.StringValue(norm),
        ExternalId:     types.StringValue(me.ExternalId),
        WorkspaceUrl:   types.StringValue(w.Config.Host),
        AclPrincipalId: types.StringValue(aclPrincipalId),
    }

    resp.Diagnostics.Append(resp.State.Set(ctx, currentUserData)...)
}
```

## 4. Key Schema Elements to Preserve

| Attribute | SDKv2 Type | Plugin Framework Type | Required/Optional/Computed |
|-----------|------------|----------------------|---------------------------|
| `id` | (implicit) | `types.String` | Computed |
| `user_name` | `schema.TypeString` | `types.String` | Computed |
| `home` | `schema.TypeString` | `types.String` | Computed |
| `repos` | `schema.TypeString` | `types.String` | Computed |
| `alphanumeric` | `schema.TypeString` | `types.String` | Computed |
| `external_id` | `schema.TypeString` | `types.String` | Computed |
| `workspace_url` | `schema.TypeString` | `types.String` | Computed |
| `acl_principal_id` | `schema.TypeString` | `types.String` | Computed |

### Compatibility Notes

Since this data source has no complex nested structures (no `types.List`, `types.Map`, or nested objects), there is no need to use the `_SdkV2` variant or call `ConfigureAsSdkV2Compatible()`. The schema is straightforward with only computed string attributes.

## 5. Testing Strategy

### 5.1 Unit Tests

Create unit tests in `data_current_user_test.go` that mirror the existing SDKv2 tests:

```go
package currentuser_test

import (
    "testing"
    // ... imports
)

func TestDataSourceCurrentUser(t *testing.T) {
    // Test with regular user email
    // Verify all computed fields are set correctly
}

func TestDataSourceCurrentUserAsSP(t *testing.T) {
    // Test with service principal (UUID)
    // Verify acl_principal_id uses "servicePrincipals/" prefix
}
```

### 5.2 Acceptance Tests

Create acceptance tests in `data_current_user_acc_test.go`:

```go
package currentuser_test

import (
    "testing"
    "github.com/databricks/terraform-provider-databricks/internal/acceptance"
)

func TestAccDataSourceCurrentUser(t *testing.T) {
    acceptance.WorkspaceLevel(t, acceptance.Step{
        Template: `
            data "databricks_current_user" "me" {}
            
            output "user_name" {
                value = data.databricks_current_user.me.user_name
            }
            output "home" {
                value = data.databricks_current_user.me.home
            }
        `,
        Check: func(s *terraform.State) error {
            // Verify outputs are populated
            return nil
        },
    })
}
```

### 5.3 Bidirectional Compatibility Tests

Following the pattern from `resource_share_acc_test.go`, create tests that verify:

#### Test 1: SDKv2 to Plugin Framework Migration

```go
func TestAccCurrentUserMigrationFromSDKv2(t *testing.T) {
    acceptance.WorkspaceLevel(t,
        // Step 1: Read using SDK v2 implementation
        acceptance.Step{
            ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
                "databricks": func() (tfprotov6.ProviderServer, error) {
                    sdkv2Provider, pluginfwProvider := acceptance.ProvidersWithDataSourceFallbacks([]string{"databricks_current_user"})
                    return providers.GetProviderServer(context.Background(), providers.WithSdkV2Provider(sdkv2Provider), providers.WithPluginFrameworkProvider(pluginfwProvider))
                },
            },
            Template: `
                data "databricks_current_user" "me" {}
            `,
        },
        // Step 2: Read using Plugin Framework (default)
        acceptance.Step{
            Template: `
                data "databricks_current_user" "me" {}
            `,
        },
    )
}
```

#### Test 2: Plugin Framework to SDKv2 Rollback

```go
func TestAccCurrentUserMigrationFromPluginFramework(t *testing.T) {
    acceptance.WorkspaceLevel(t,
        // Step 1: Read using Plugin Framework (default)
        acceptance.Step{
            Template: `
                data "databricks_current_user" "me" {}
            `,
        },
        // Step 2: Read using SDK v2 implementation
        acceptance.Step{
            ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
                "databricks": func() (tfprotov6.ProviderServer, error) {
                    sdkv2Provider, pluginfwProvider := acceptance.ProvidersWithDataSourceFallbacks([]string{"databricks_current_user"})
                    return providers.GetProviderServer(context.Background(), providers.WithSdkV2Provider(sdkv2Provider), providers.WithPluginFrameworkProvider(pluginfwProvider))
                },
            },
            Template: `
                data "databricks_current_user" "me" {}
            `,
        },
    )
}
```

### 5.4 Schema Verification

Run `make diff-schema` before and after the migration to ensure no breaking schema changes.

## 6. Step-by-Step Migration Process

### Phase 1: Preparation

1. **Create the directory structure**
   ```bash
   mkdir -p internal/providers/pluginfw/products/currentuser
   ```

2. **Review existing SDKv2 implementation**
   - File: `scim/data_current_user.go`
   - Understand all schema fields and their types
   - Document the Read logic

### Phase 2: Implementation

3. **Create the Plugin Framework data source**
   - Create `internal/providers/pluginfw/products/currentuser/data_current_user.go`
   - Implement `CurrentUserData` struct with all fields
   - Implement `ApplySchemaCustomizations` method
   - Implement `GetComplexFieldTypes` method (empty for this simple case)
   - Implement `Metadata`, `Schema`, `Configure`, and `Read` methods

4. **Preserve exact behavior**
   - Ensure `acl_principal_id` logic matches (UUID check for service principals)
   - Ensure `alphanumeric` transformation matches (lowercase, non-alphanumeric to `_`)
   - Ensure `home` and `repos` path formatting matches

### Phase 3: Registration

5. **Add to migrated data sources list**
   - Edit `internal/providers/pluginfw/pluginfw_rollout_utils.go`
   - Add import: `"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/products/currentuser"`
   - Add to `migratedDataSources` slice (keep sorted alphabetically):
     ```go
     var migratedDataSources = []func() datasource.DataSource{
         currentuser.DataSourceCurrentUser,  // Add this line
         sharing.DataSourceShare,
         sharing.DataSourceShares,
         volume.DataSourceVolumes,
     }
     ```

### Phase 4: Testing

6. **Create unit tests**
   - Create `internal/providers/pluginfw/products/currentuser/data_current_user_test.go`
   - Port existing tests from `scim/data_current_user_test.go`

7. **Create acceptance tests**
   - Create `internal/providers/pluginfw/products/currentuser/data_current_user_acc_test.go`
   - Include basic functionality tests
   - Include bidirectional compatibility tests

8. **Run schema verification**
   ```bash
   make diff-schema
   ```
   - Verify no breaking changes in the schema output

9. **Run all tests**
   ```bash
   # Unit tests
   go test -v ./internal/providers/pluginfw/products/currentuser/...
   
   # Lint
   make lint
   
   # Integration tests (requires credentials)
   go test -v -run TestAccDataSourceCurrentUser ./internal/providers/pluginfw/products/currentuser/...
   ```

### Phase 5: Validation

10. **Manual testing**
    - Build the provider locally: `make install`
    - Test with a simple Terraform configuration:
      ```hcl
      terraform {
        required_providers {
          databricks = {
            source = "databricks/databricks"
          }
        }
      }
      
      provider "databricks" {}
      
      data "databricks_current_user" "me" {}
      
      output "current_user" {
        value = data.databricks_current_user.me
      }
      ```
    - Verify all outputs match expected values

11. **Test rollback capability**
    - Set environment variable: `USE_SDK_V2_DATA_SOURCES=databricks_current_user`
    - Verify the data source still works with SDKv2 implementation

### Phase 6: Documentation & PR

12. **Update NEXT_CHANGELOG.md**
    ```markdown
    ## [Version]
    
    ### Internal Changes
    * Migrated `databricks_current_user` data source to Plugin Framework ([#PR](link))
    ```

13. **Create Pull Request**
    - Include all new files
    - Reference this migration plan
    - Ensure CI passes

## 7. Rollback Plan

If issues are discovered after deployment:

1. **Immediate rollback**: Users can set `USE_SDK_V2_DATA_SOURCES=databricks_current_user` environment variable to force SDKv2 usage

2. **Code rollback**: Remove the data source from `migratedDataSources` list in `pluginfw_rollout_utils.go`

## 8. Files to Create/Modify

### New Files
- `internal/providers/pluginfw/products/currentuser/data_current_user.go`
- `internal/providers/pluginfw/products/currentuser/data_current_user_test.go`
- `internal/providers/pluginfw/products/currentuser/data_current_user_acc_test.go`

### Modified Files
- `internal/providers/pluginfw/pluginfw_rollout_utils.go` (add import and registration)
- `NEXT_CHANGELOG.md` (add changelog entry)

### Files to Keep (SDKv2 - for fallback)
- `scim/data_current_user.go` (keep for rollback support)
- `scim/data_current_user_test.go` (keep for reference)

## 9. Success Criteria

- [ ] All unit tests pass
- [ ] All acceptance tests pass
- [ ] `make diff-schema` shows no breaking changes
- [ ] `make lint` passes
- [ ] Bidirectional compatibility tests pass (SDKv2 ↔ Plugin Framework)
- [ ] Manual testing confirms identical behavior
- [ ] CI pipeline passes
- [ ] PR approved and merged

## 10. References

- **Existing migrations for reference**:
  - `internal/providers/pluginfw/products/sharing/data_share.go`
  - `internal/providers/pluginfw/products/sharing/data_shares.go`
  - `internal/providers/pluginfw/products/volume/data_volumes.go`
  
- **Contributing guidelines**: `CONTRIBUTING.md` (lines 160-220)

- **Plugin Framework documentation**: https://developer.hashicorp.com/terraform/plugin/framework
