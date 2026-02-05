package sql

import (
	"context"
	"reflect"
	"sort"
	"strings"

	"github.com/databricks/databricks-sdk-go/service/sql"
	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const dataSourceName = "sql_warehouses"

func DataSourceSqlWarehouses() datasource.DataSource {
	return &SqlWarehousesDataSource{}
}

var _ datasource.DataSourceWithConfigure = &SqlWarehousesDataSource{}

type SqlWarehousesDataSource struct {
	Client *common.DatabricksClient
}

type SqlWarehousesList struct {
	WarehouseNameContains types.String `tfsdk:"warehouse_name_contains"`
	Ids                   types.Set    `tfsdk:"ids"`
	tfschema.Namespace
}

func (SqlWarehousesList) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["warehouse_name_contains"] = attrs["warehouse_name_contains"].SetOptional()
	attrs["ids"] = attrs["ids"].SetOptional().SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (SqlWarehousesList) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"ids":             reflect.TypeOf(types.String{}),
		"provider_config": reflect.TypeOf(tfschema.ProviderConfigData{}),
	}
}

func (d *SqlWarehousesDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceName)
}

func (d *SqlWarehousesDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, SqlWarehousesList{}, nil)
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *SqlWarehousesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *SqlWarehousesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceName)

	var data SqlWarehousesList
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceIDDataSource(ctx, data.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, clientDiags := d.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(clientDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	list, err := w.Warehouses.ListAll(ctx, sql.ListWarehousesRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch SQL warehouses", err.Error())
		return
	}

	nameContains := strings.ToLower(data.WarehouseNameContains.ValueString())
	ids := []attr.Value{}
	for _, warehouse := range list {
		if nameContains != "" && !strings.Contains(strings.ToLower(warehouse.Name), nameContains) {
			continue
		}
		ids = append(ids, types.StringValue(warehouse.Id))
	}

	sort.Slice(ids, func(i, j int) bool {
		return ids[i].(types.String).ValueString() < ids[j].(types.String).ValueString()
	})

	data.Ids = types.SetValueMust(types.StringType, ids)
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
