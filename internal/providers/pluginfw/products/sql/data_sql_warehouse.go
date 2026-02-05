package sql

import (
	"context"
	"fmt"
	"reflect"

	"github.com/databricks/databricks-sdk-go/service/sql"
	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/converters"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/databricks/terraform-provider-databricks/internal/service/sql_tf"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const dataSourceName = "sql_warehouse"

func DataSourceSqlWarehouse() datasource.DataSource {
	return &SqlWarehouseDataSource{}
}

var _ datasource.DataSourceWithConfigure = &SqlWarehouseDataSource{}

type SqlWarehouseDataSource struct {
	Client *common.DatabricksClient
}

type SqlWarehouseData struct {
	sql_tf.GetWarehouseResponse_SdkV2
	tfschema.Namespace_SdkV2

	// The data source ID is not part of the endpoint API response.
	// We manually resolve it by retrieving the list of data sources
	// and matching this entity's endpoint ID.
	DataSourceId types.String `tfsdk:"data_source_id"`
}

func (s SqlWarehouseData) GetComplexFieldTypes(ctx context.Context) map[string]reflect.Type {
	types := s.GetWarehouseResponse_SdkV2.GetComplexFieldTypes(ctx)
	types["provider_config"] = reflect.TypeOf(tfschema.ProviderConfig{})
	return types
}

func (s SqlWarehouseData) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	s.GetWarehouseResponse_SdkV2.ApplySchemaCustomizations(attrs)
	attrs["data_source_id"] = attrs["data_source_id"].SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	// id and name are both computed/optional because users can specify either to retrieve the warehouse
	attrs["id"] = attrs["id"].SetOptional().SetComputed()
	attrs["name"] = attrs["name"].SetOptional().SetComputed()
	return attrs
}

func (d *SqlWarehouseDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceName)
}

func (d *SqlWarehouseDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, SqlWarehouseData{}, func(c tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		c.ConfigureAsSdkV2Compatible()
		// Ensure provider_config list has at most 1 element
		c.AddValidator(listvalidator.SizeAtMost(1), "provider_config")
		return c
	})
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *SqlWarehouseDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *SqlWarehouseDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceName)

	var config SqlWarehouseData
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := config.Id.ValueString()
	name := config.Name.ValueString()

	if id == "" && name == "" {
		resp.Diagnostics.AddError("Invalid configuration", "either 'id' or 'name' should be provided")
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceID_SdkV2(ctx, config.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, clientDiags := d.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(clientDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// List data sources to find the warehouse by name or ID
	dataSources, err := w.DataSources.List(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list data sources", err.Error())
		return
	}

	var selected []sql.DataSource
	for _, source := range dataSources {
		if name != "" && source.Name == name {
			selected = append(selected, source)
		} else if id != "" && source.WarehouseId == id {
			selected = append(selected, source)
			break
		}
	}

	if len(selected) == 0 {
		if name != "" {
			resp.Diagnostics.AddError("SQL warehouse not found", fmt.Sprintf("can't find SQL warehouse with the name '%s'", name))
		} else {
			resp.Diagnostics.AddError("SQL warehouse not found", fmt.Sprintf("can't find SQL warehouse with the ID '%s'", id))
		}
		return
	}

	if len(selected) > 1 {
		if name != "" {
			resp.Diagnostics.AddError("Multiple SQL warehouses found", fmt.Sprintf("there are multiple SQL warehouses with the name '%s'", name))
		} else {
			resp.Diagnostics.AddError("Multiple SQL warehouses found", fmt.Sprintf("there are multiple SQL warehouses with the ID '%s'", id))
		}
		return
	}

	// Get the full warehouse details
	warehouse, err := w.Warehouses.GetById(ctx, selected[0].WarehouseId)
	if err != nil {
		resp.Diagnostics.AddError("Failed to get SQL warehouse", err.Error())
		return
	}

	var newState SqlWarehouseData
	resp.Diagnostics.Append(converters.GoSdkToTfSdkStruct(ctx, warehouse, &newState)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Set the data source ID (not part of the warehouse API response)
	newState.DataSourceId = types.StringValue(selected[0].Id)

	// Preserve the provider config from the request
	newState.Namespace_SdkV2 = tfschema.Namespace_SdkV2{
		ProviderConfig: config.ProviderConfig,
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}
