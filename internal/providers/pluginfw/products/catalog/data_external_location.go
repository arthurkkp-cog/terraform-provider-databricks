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

const dataSourceNameExternalLocation = "external_location"

func DataSourceExternalLocation() datasource.DataSource {
	return &ExternalLocationDataSource{}
}

var _ datasource.DataSourceWithConfigure = &ExternalLocationDataSource{}

type ExternalLocationDataSource struct {
	Client *common.DatabricksClient
}

type ExternalLocationData struct {
	Id               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	ExternalLocation types.List   `tfsdk:"external_location_info"`
	tfschema.Namespace
}

func (ExternalLocationData) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["id"] = attrs["id"].SetOptional().SetComputed()
	attrs["name"] = attrs["name"].SetRequired()
	attrs["external_location_info"] = attrs["external_location_info"].SetOptional().SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (ExternalLocationData) GetComplexFieldTypes(ctx context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"external_location_info": reflect.TypeOf(catalog_tf.ExternalLocationInfo{}),
		"provider_config":        reflect.TypeOf(tfschema.ProviderConfigData{}),
	}
}

func (d *ExternalLocationDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceNameExternalLocation)
}

func (d *ExternalLocationDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, ExternalLocationData{}, func(cs tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		cs.ConfigureAsSdkV2Compatible()
		return cs
	})
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *ExternalLocationDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *ExternalLocationDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceNameExternalLocation)

	var data ExternalLocationData
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceIDDataSource(ctx, data.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := d.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	location, err := w.ExternalLocations.GetByName(ctx, data.Name.ValueString())
	if err != nil {
		if apierr.IsMissing(err) {
			resp.State.RemoveResource(ctx)
		}
		resp.Diagnostics.AddError("Failed to fetch external location", err.Error())
		return
	}

	var externalLocationInfo catalog_tf.ExternalLocationInfo
	resp.Diagnostics.Append(converters.GoSdkToTfSdkStruct(ctx, location, &externalLocationInfo)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ExternalLocation = types.ListValueMust(externalLocationInfo.Type(ctx), []attr.Value{externalLocationInfo.ToObjectValue(ctx)})
	data.Id = types.StringValue(location.Name)

	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
