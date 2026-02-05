package volume

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

const dataSourceNameVolume = "volume"

func DataSourceVolume() datasource.DataSource {
	return &VolumeDataSource{}
}

var _ datasource.DataSourceWithConfigure = &VolumeDataSource{}

type VolumeDataSource struct {
	Client *common.DatabricksClient
}

type VolumeData_SdkV2 struct {
	Id         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	VolumeInfo types.List   `tfsdk:"volume_info"`
	tfschema.Namespace_SdkV2
}

func (VolumeData_SdkV2) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["id"] = attrs["id"].SetComputed()
	attrs["name"] = attrs["name"].SetRequired()
	attrs["volume_info"] = attrs["volume_info"].SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (VolumeData_SdkV2) GetComplexFieldTypes(ctx context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"volume_info":     reflect.TypeOf(catalog_tf.VolumeInfo_SdkV2{}),
		"provider_config": reflect.TypeOf(tfschema.ProviderConfigData_SdkV2{}),
	}
}

func (d *VolumeDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceNameVolume)
}

func (d *VolumeDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, VolumeData_SdkV2{}, func(cs tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		cs.ConfigureAsSdkV2Compatible()
		return cs
	})
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *VolumeDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *VolumeDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceNameVolume)

	var data VolumeData_SdkV2
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

	volume, err := w.Volumes.ReadByName(ctx, data.Name.ValueString())
	if err != nil {
		if apierr.IsMissing(err) {
			resp.State.RemoveResource(ctx)
		}
		resp.Diagnostics.AddError("Failed to fetch volume", err.Error())
		return
	}

	var volumeInfoTfSdk catalog_tf.VolumeInfo_SdkV2
	resp.Diagnostics.Append(converters.GoSdkToTfSdkStruct(ctx, volume, &volumeInfoTfSdk)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Id = types.StringValue(volume.FullName)
	data.VolumeInfo = types.ListValueMust(volumeInfoTfSdk.Type(ctx), []attr.Value{volumeInfoTfSdk.ToObjectValue(ctx)})

	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
