package catalog

import (
	"context"
	"fmt"
	"reflect"

	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const dataSourceNameMetastores = "metastores"

func DataSourceMetastores() datasource.DataSource {
	return &MetastoresDataSource{}
}

var _ datasource.DataSourceWithConfigure = &MetastoresDataSource{}

type MetastoresDataSource struct {
	Client *common.DatabricksClient
}

type MetastoresData struct {
	Ids types.Map `tfsdk:"ids"`
}

func (MetastoresData) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["ids"] = attrs["ids"].SetComputed()
	return attrs
}

func (MetastoresData) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"ids": reflect.TypeOf(types.String{}),
	}
}

func (d *MetastoresDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceNameMetastores)
}

func (d *MetastoresDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, MetastoresData{}, nil)
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *MetastoresDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *MetastoresDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceNameMetastores)

	var data MetastoresData
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	acc, diags := d.Client.GetAccountClient()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	metastores, err := acc.Metastores.ListAll(ctx)
	if err != nil {
		resp.Diagnostics.AddError("failed to list metastores", err.Error())
		return
	}

	ids := map[string]string{}
	for _, v := range metastores {
		name := v.Name
		if _, duplicateName := ids[name]; duplicateName {
			resp.Diagnostics.AddError("duplicate metastore name detected", fmt.Sprintf("duplicate metastore name detected: %s", name))
			return
		}
		ids[name] = v.MetastoreId
	}

	idsMap, diags := types.MapValueFrom(ctx, types.StringType, ids)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Ids = idsMap
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
