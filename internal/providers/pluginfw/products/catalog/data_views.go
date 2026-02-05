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
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/converters"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const dataSourceNameViews = "views"

func DataSourceViews() datasource.DataSource {
	return &ViewsDataSource{}
}

var _ datasource.DataSourceWithConfigure = &ViewsDataSource{}

type ViewsDataSource struct {
	Client *common.DatabricksClient
}

type ViewsData struct {
	CatalogName types.String `tfsdk:"catalog_name"`
	SchemaName  types.String `tfsdk:"schema_name"`
	Ids         types.List   `tfsdk:"ids"`
	tfschema.Namespace
}

func (ViewsData) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["catalog_name"] = attrs["catalog_name"].SetRequired()
	attrs["schema_name"] = attrs["schema_name"].SetRequired()
	attrs["ids"] = attrs["ids"].SetOptional().SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (ViewsData) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"ids":             reflect.TypeOf(types.String{}),
		"provider_config": reflect.TypeOf(tfschema.ProviderConfigData{}),
	}
}

func (d *ViewsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceNameViews)
}

func (d *ViewsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, ViewsData{}, nil)
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *ViewsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *ViewsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceNameViews)

	var viewsData ViewsData
	diags := req.Config.Get(ctx, &viewsData)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var listTablesRequest catalog.ListTablesRequest
	converters.TfSdkToGoSdkStruct(ctx, viewsData, &listTablesRequest)

	workspaceID, diags := tfschema.GetWorkspaceIDDataSource(ctx, viewsData.ProviderConfig)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	w, clientDiags := d.Client.GetWorkspaceClientForUnifiedProviderWithDiagnostics(ctx, workspaceID)
	resp.Diagnostics.Append(clientDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tables, err := w.Tables.ListAll(ctx, listTablesRequest)
	if err != nil {
		if apierr.IsMissing(err) {
			resp.State.RemoveResource(ctx)
		}
		resp.Diagnostics.AddError(fmt.Sprintf("failed to get views for the catalog:%s and schema:%s", listTablesRequest.CatalogName, listTablesRequest.SchemaName), err.Error())
		return
	}

	ids := []attr.Value{}
	for _, v := range tables {
		if v.TableType == "VIEW" {
			ids = append(ids, types.StringValue(v.FullName))
		}
	}
	viewsData.Ids = types.ListValueMust(types.StringType, ids)
	resp.Diagnostics.Append(resp.State.Set(ctx, viewsData)...)
}
