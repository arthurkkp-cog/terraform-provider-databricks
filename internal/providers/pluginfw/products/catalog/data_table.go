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
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const dataSourceNameTable = "table"

func DataSourceTable() datasource.DataSource {
	return &TableDataSource{}
}

var _ datasource.DataSourceWithConfigure = &TableDataSource{}

type TableDataSource struct {
	Client *common.DatabricksClient
}

type TableData struct {
	Id        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	TableInfo types.List   `tfsdk:"table_info"`
	tfschema.Namespace_SdkV2
}

func (TableData) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["id"] = attrs["id"].SetOptional().SetComputed()
	attrs["name"] = attrs["name"].SetRequired()
	attrs["table_info"] = attrs["table_info"].SetOptional().SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (TableData) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"table_info":      reflect.TypeOf(catalog_tf.TableInfo_SdkV2{}),
		"provider_config": reflect.TypeOf(tfschema.ProviderConfig{}),
	}
}

func (d *TableDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceNameTable)
}

func (d *TableDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, TableData{}, func(c tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		c.ConfigureAsSdkV2Compatible()
		c.AddValidator(listvalidator.SizeAtMost(1), "table_info")
		c.AddValidator(listvalidator.SizeAtMost(1), "provider_config")
		return c
	})
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *TableDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *TableDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceNameTable)

	var data TableData
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

	table, err := w.Tables.GetByFullName(ctx, data.Name.ValueString())
	if err != nil {
		if apierr.IsMissing(err) {
			resp.State.RemoveResource(ctx)
		}
		resp.Diagnostics.AddError("failed to get table", err.Error())
		return
	}

	var tableInfoTfSdk catalog_tf.TableInfo_SdkV2
	resp.Diagnostics.Append(converters.GoSdkToTfSdkStruct(ctx, table, &tableInfoTfSdk)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Id = types.StringValue(table.TableId)
	data.TableInfo = types.ListValueMust(catalog_tf.TableInfo_SdkV2{}.Type(ctx), []attr.Value{tableInfoTfSdk.ToObjectValue(ctx)})

	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
