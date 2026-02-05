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

const dataSourceNameSchemas = "schemas"

func DataSourceSchemas() datasource.DataSource {
	return &SchemasDataSource{}
}

var _ datasource.DataSourceWithConfigure = &SchemasDataSource{}

type SchemasDataSource struct {
	Client *common.DatabricksClient
}

type SchemasList struct {
	CatalogName types.String `tfsdk:"catalog_name"`
	Ids         types.List   `tfsdk:"ids"`
	tfschema.Namespace
}

func (SchemasList) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["catalog_name"] = attrs["catalog_name"].SetRequired()
	attrs["ids"] = attrs["ids"].SetOptional().SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (SchemasList) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"ids":             reflect.TypeOf(types.String{}),
		"provider_config": reflect.TypeOf(tfschema.ProviderConfigData{}),
	}
}

func (d *SchemasDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceNameSchemas)
}

func (d *SchemasDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, SchemasList{}, nil)
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *SchemasDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *SchemasDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceNameSchemas)

	var schemasList SchemasList
	diags := req.Config.Get(ctx, &schemasList)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID, diags := tfschema.GetWorkspaceIDDataSource(ctx, schemasList.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, clientDiags := d.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(clientDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	catalogName := schemasList.CatalogName.ValueString()
	schemas, err := w.Schemas.ListAll(ctx, catalog.ListSchemasRequest{CatalogName: catalogName})
	if err != nil {
		if apierr.IsMissing(err) {
			resp.State.RemoveResource(ctx)
		}
		resp.Diagnostics.AddError(fmt.Sprintf("failed to get schemas for catalog: %s", catalogName), err.Error())
		return
	}

	ids := []attr.Value{}
	for _, v := range schemas {
		ids = append(ids, types.StringValue(v.FullName))
	}
	schemasList.Ids = types.ListValueMust(types.StringType, ids)
	resp.Diagnostics.Append(resp.State.Set(ctx, schemasList)...)
}
