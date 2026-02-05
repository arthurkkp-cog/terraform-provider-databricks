package catalog

import (
	"context"
	"reflect"

	"github.com/databricks/databricks-sdk-go/service/catalog"
	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const dataSourceNameStorageCredentials = "storage_credentials"

func DataSourceStorageCredentials() datasource.DataSource {
	return &StorageCredentialsDataSource{}
}

var _ datasource.DataSourceWithConfigure = &StorageCredentialsDataSource{}

type StorageCredentialsDataSource struct {
	Client *common.DatabricksClient
}

type StorageCredentialsList struct {
	Names types.List `tfsdk:"names"`
	tfschema.Namespace
}

func (StorageCredentialsList) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"names":           reflect.TypeOf(types.String{}),
		"provider_config": reflect.TypeOf(tfschema.ProviderConfigData{}),
	}
}

func (StorageCredentialsList) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["names"] = attrs["names"].SetComputed().SetOptional()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (d *StorageCredentialsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceNameStorageCredentials)
}

func (d *StorageCredentialsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, StorageCredentialsList{}, nil)
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *StorageCredentialsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *StorageCredentialsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceNameStorageCredentials)

	var config StorageCredentialsList
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceIDDataSource(ctx, config.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, clientDiags := d.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(clientDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	credentials, err := w.StorageCredentials.ListAll(ctx, catalog.ListStorageCredentialsRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch storage credentials", err.Error())
		return
	}

	names := make([]attr.Value, len(credentials))
	for i, credential := range credentials {
		names[i] = types.StringValue(credential.Name)
	}

	newState := StorageCredentialsList{
		Names: types.ListValueMust(types.StringType, names),
		Namespace: tfschema.Namespace{
			ProviderConfig: config.ProviderConfig,
		},
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}
