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

const dataSourceNameCatalog = "catalog"

func DataSourceCatalog() datasource.DataSource {
	return &CatalogDataSource{}
}

var _ datasource.DataSourceWithConfigure = &CatalogDataSource{}

type CatalogDataSource struct {
	Client *common.DatabricksClient
}

type CatalogData struct {
	Id          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	CatalogInfo types.List   `tfsdk:"catalog_info"`
	tfschema.Namespace_SdkV2
}

func (CatalogData) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["id"] = attrs["id"].SetOptional().SetComputed()
	attrs["name"] = attrs["name"].SetRequired()
	attrs["catalog_info"] = attrs["catalog_info"].SetOptional().SetComputed()
	attrs["catalog_info"] = attrs["catalog_info"].(tfschema.ListNestedAttributeBuilder).AddValidator(listvalidator.SizeAtMost(1)).(tfschema.AttributeBuilder)
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (CatalogData) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"catalog_info":    reflect.TypeOf(catalog_tf.CatalogInfo_SdkV2{}),
		"provider_config": reflect.TypeOf(tfschema.ProviderConfig{}),
	}
}

func (d *CatalogDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceNameCatalog)
}

func (d *CatalogDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, CatalogData{}, func(c tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		c.ConfigureAsSdkV2Compatible()
		return c
	})
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *CatalogDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *CatalogDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceNameCatalog)

	var data CatalogData
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

	catalogSdk, err := w.Catalogs.GetByName(ctx, data.Name.ValueString())
	if err != nil {
		if apierr.IsMissing(err) {
			resp.State.RemoveResource(ctx)
		}
		resp.Diagnostics.AddError("failed to get catalog", err.Error())
		return
	}

	var catalogInfoTfSdk catalog_tf.CatalogInfo_SdkV2
	resp.Diagnostics.Append(converters.GoSdkToTfSdkStruct(ctx, catalogSdk, &catalogInfoTfSdk)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.CatalogInfo = types.ListValueMust(catalogInfoTfSdk.Type(ctx), []attr.Value{catalogInfoTfSdk.ToObjectValue(ctx)})
	data.Id = types.StringValue(catalogSdk.Name)

	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
