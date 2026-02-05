package catalog

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/databricks/databricks-sdk-go/service/catalog"
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

const dataSourceNameMetastore = "metastore"

func DataSourceMetastore() datasource.DataSource {
	return &MetastoreDataSource{}
}

var _ datasource.DataSourceWithConfigure = &MetastoreDataSource{}

type MetastoreDataSource struct {
	Client *common.DatabricksClient
}

type MetastoreData_SdkV2 struct {
	Id          types.String `tfsdk:"id"`
	MetastoreId types.String `tfsdk:"metastore_id"`
	Name        types.String `tfsdk:"name"`
	Region      types.String `tfsdk:"region"`
	Metastore   types.List   `tfsdk:"metastore_info"`
}

func (MetastoreData_SdkV2) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["id"] = attrs["id"].SetComputed()
	attrs["metastore_id"] = attrs["metastore_id"].SetOptional().SetComputed()
	attrs["name"] = attrs["name"].SetOptional().SetComputed()
	attrs["region"] = attrs["region"].SetOptional().SetComputed()
	attrs["metastore_info"] = attrs["metastore_info"].SetOptional().SetComputed()
	return attrs
}

func (MetastoreData_SdkV2) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"metastore_info": reflect.TypeOf(catalog_tf.MetastoreInfo_SdkV2{}),
	}
}

func (d *MetastoreDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceNameMetastore)
}

func (d *MetastoreDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, MetastoreData_SdkV2{}, func(cs tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		cs.ConfigureAsSdkV2Compatible()
		return cs
	})
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *MetastoreDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *MetastoreDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceNameMetastore)

	var data MetastoreData_SdkV2
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	acc, diags := d.Client.GetAccountClient()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	metastoreId := data.MetastoreId.ValueString()
	name := data.Name.ValueString()
	region := data.Region.ValueString()

	if metastoreId == "" && name == "" && region == "" {
		resp.Diagnostics.AddError("Invalid configuration", "one of metastore_id, name or region must be provided")
		return
	}
	if (metastoreId != "" && name != "") || (region != "" && metastoreId != "") || (region != "" && name != "") {
		resp.Diagnostics.AddError("Invalid configuration", "only one of metastore_id, name or region must be provided")
		return
	}

	var metastoreInfo *catalog.MetastoreInfo
	if metastoreId != "" {
		minfo, err := acc.Metastores.GetByMetastoreId(ctx, metastoreId)
		if err != nil {
			resp.Diagnostics.AddError("Failed to get metastore", err.Error())
			return
		}
		metastoreInfo = minfo.MetastoreInfo
	} else {
		metastores, err := acc.Metastores.ListAll(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Failed to list metastores", err.Error())
			return
		}
		minfos := []catalog.MetastoreInfo{}
		if name != "" {
			for _, v := range metastores {
				if strings.EqualFold(v.Name, name) {
					minfos = append(minfos, v)
				}
			}
		} else {
			for _, v := range metastores {
				if strings.EqualFold(v.Region, region) {
					minfos = append(minfos, v)
				}
			}
		}
		if len(minfos) == 0 {
			resp.Diagnostics.AddError("Metastore not found", fmt.Sprintf("a metastore with name '%s' or in region '%s' is not found", name, region))
			return
		}
		if len(minfos) > 1 {
			resp.Diagnostics.AddError("Multiple metastores found", fmt.Sprintf("there are %d metastores with name '%s' in region '%s'", len(minfos), name, region))
			return
		}
		metastoreInfo = &minfos[0]
	}

	var metastoreInfoTf catalog_tf.MetastoreInfo_SdkV2
	resp.Diagnostics.Append(converters.GoSdkToTfSdkStruct(ctx, metastoreInfo, &metastoreInfoTf)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Id = types.StringValue(metastoreInfo.MetastoreId)
	data.MetastoreId = types.StringValue(metastoreInfo.MetastoreId)
	data.Name = types.StringValue(metastoreInfo.Name)
	data.Region = types.StringValue(metastoreInfo.Region)
	data.Metastore = types.ListValueMust(catalog_tf.MetastoreInfo_SdkV2{}.Type(ctx), []attr.Value{metastoreInfoTf.ToObjectValue(ctx)})

	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
