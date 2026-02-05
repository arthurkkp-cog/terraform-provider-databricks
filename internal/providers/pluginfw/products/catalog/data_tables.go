package catalog

import (
	"context"
	"fmt"
	"reflect"

	"github.com/databricks/databricks-sdk-go/apierr"
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

const dataSourceNameTables = "tables"

func DataSourceTables() datasource.DataSource {
	return &TablesDataSource{}
}

var _ datasource.DataSourceWithConfigure = &TablesDataSource{}

type TablesDataSource struct {
	Client *common.DatabricksClient
}

type TablesList struct {
	CatalogName types.String `tfsdk:"catalog_name"`
	SchemaName  types.String `tfsdk:"schema_name"`
	Ids         types.List   `tfsdk:"ids"`
	tfschema.Namespace
}

func (TablesList) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["catalog_name"] = attrs["catalog_name"].SetRequired()
	attrs["schema_name"] = attrs["schema_name"].SetRequired()
	attrs["ids"] = attrs["ids"].SetOptional().SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (TablesList) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"ids":             reflect.TypeOf(types.String{}),
		"provider_config": reflect.TypeOf(tfschema.ProviderConfigData{}),
	}
}

func (d *TablesDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceNameTables)
}

func (d *TablesDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, TablesList{}, nil)
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *TablesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *TablesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceNameTables)

	var tablesList TablesList
	diags := req.Config.Get(ctx, &tablesList)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceIDDataSource(ctx, tablesList.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, diags := d.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	catalogName := tablesList.CatalogName.ValueString()
	schemaName := tablesList.SchemaName.ValueString()
	tables, err := w.Tables.ListAll(ctx, catalog.ListTablesRequest{
		CatalogName: catalogName,
		SchemaName:  schemaName,
	})
	if err != nil {
		if apierr.IsMissing(err) {
			resp.State.RemoveResource(ctx)
		}
		resp.Diagnostics.AddError(fmt.Sprintf("failed to get tables for %s.%s schema", catalogName, schemaName), err.Error())
		return
	}

	ids := []attr.Value{}
	for _, v := range tables {
		if v.TableType != catalog.TableTypeView {
			ids = append(ids, types.StringValue(v.FullName))
		}
	}
	tablesList.Ids = types.ListValueMust(types.StringType, ids)
	resp.Diagnostics.Append(resp.State.Set(ctx, tablesList)...)
}
