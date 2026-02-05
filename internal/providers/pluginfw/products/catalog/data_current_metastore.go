package catalog

import (
	"context"
	"reflect"
	"strings"

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

const dataSourceNameCurrentMetastore = "current_metastore"

func DataSourceCurrentMetastore() datasource.DataSource {
	return &CurrentMetastoreDataSource{}
}

var _ datasource.DataSourceWithConfigure = &CurrentMetastoreDataSource{}

type CurrentMetastoreDataSource struct {
	Client *common.DatabricksClient
}

type CurrentMetastoreData struct {
	Id        types.String `tfsdk:"id"`
	Metastore types.List   `tfsdk:"metastore_info"`
	tfschema.Namespace_SdkV2
}

var _ pluginfwcommon.ComplexFieldTypeProvider = CurrentMetastoreData{}

func (CurrentMetastoreData) GetComplexFieldTypes(ctx context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"metastore_info":  reflect.TypeOf(catalog_tf.GetMetastoreSummaryResponse_SdkV2{}),
		"provider_config": reflect.TypeOf(tfschema.ProviderConfig{}),
	}
}

func (CurrentMetastoreData) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["id"] = attrs["id"].SetOptional().SetComputed()
	attrs["metastore_info"] = attrs["metastore_info"].SetOptional().SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (d *CurrentMetastoreDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceNameCurrentMetastore)
}

func (d *CurrentMetastoreDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, CurrentMetastoreData{}, func(cs tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		cs.ConfigureAsSdkV2Compatible()
		return cs
	})
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *CurrentMetastoreDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *CurrentMetastoreDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceNameCurrentMetastore)

	var data CurrentMetastoreData
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, data.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := d.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	summary, err := w.Metastores.Summary(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "No metastore assigned for the current workspace") {
			data.Metastore = types.ListNull(catalog_tf.GetMetastoreSummaryResponse_SdkV2{}.Type(ctx))
			data.Id = types.StringValue("no_metastore")
			resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
			return
		}
		resp.Diagnostics.AddError("Failed to get current metastore summary", err.Error())
		return
	}

	var metastoreInfo catalog_tf.GetMetastoreSummaryResponse_SdkV2
	resp.Diagnostics.Append(converters.GoSdkToTfSdkStruct(ctx, summary, &metastoreInfo)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Metastore = types.ListValueMust(catalog_tf.GetMetastoreSummaryResponse_SdkV2{}.Type(ctx), []attr.Value{metastoreInfo.ToObjectValue(ctx)})
	data.Id = types.StringValue(summary.MetastoreId)
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
