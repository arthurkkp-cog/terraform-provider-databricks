package catalog

import (
	"context"
	"reflect"

	"github.com/databricks/databricks-sdk-go/apierr"
	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/converters"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/databricks/terraform-provider-databricks/internal/service/catalog_tf"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const dataSourceNameStorageCredential = "storage_credential"

func DataSourceStorageCredential() datasource.DataSource {
	return &StorageCredentialDataSource{}
}

var _ datasource.DataSourceWithConfigure = &StorageCredentialDataSource{}

type StorageCredentialDataSource struct {
	Client *common.DatabricksClient
}

type StorageCredentialData struct {
	Id                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	StorageCredential types.List   `tfsdk:"storage_credential_info"`
	tfschema.Namespace_SdkV2
}

func (StorageCredentialData) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["id"] = attrs["id"].SetOptional().SetComputed()
	attrs["name"] = attrs["name"].SetRequired()
	attrs["storage_credential_info"] = attrs["storage_credential_info"].SetOptional().SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (StorageCredentialData) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"storage_credential_info": reflect.TypeOf(catalog_tf.StorageCredentialInfo_SdkV2{}),
		"provider_config":         reflect.TypeOf(tfschema.ProviderConfigData_SdkV2{}),
	}
}

func (d *StorageCredentialDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceNameStorageCredential)
}

func (d *StorageCredentialDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, StorageCredentialData{}, func(cs tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		cs.ConfigureAsSdkV2Compatible()
		return cs
	})
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *StorageCredentialDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *StorageCredentialDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceNameStorageCredential)

	var data StorageCredentialData
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceIDDataSource_SdkV2(ctx, data.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := d.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	credential, err := w.StorageCredentials.GetByName(ctx, data.Name.ValueString())
	if err != nil {
		if apierr.IsMissing(err) {
			resp.State.RemoveResource(ctx)
		}
		resp.Diagnostics.AddError("Failed to fetch storage credential", err.Error())
		return
	}

	var credentialTfSdk catalog_tf.StorageCredentialInfo_SdkV2
	resp.Diagnostics.Append(converters.GoSdkToTfSdkStruct(ctx, credential, &credentialTfSdk)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Id = types.StringValue(credential.Id)
	data.StorageCredential = types.ListValueMust(credentialTfSdk.Type(ctx), []attr.Value{credentialTfSdk.ToObjectValue(ctx)})
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
