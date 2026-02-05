package storage

import (
	"context"
	"reflect"

	"github.com/databricks/terraform-provider-databricks/common"
	pluginfwcommon "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/common"
	pluginfwcontext "github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/context"
	"github.com/databricks/terraform-provider-databricks/internal/providers/pluginfw/tfschema"
	"github.com/databricks/terraform-provider-databricks/storage"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const dataSourceName = "dbfs_file_paths"

func DataSourceDbfsFilePaths() datasource.DataSource {
	return &DbfsFilePathsDataSource{}
}

var _ datasource.DataSourceWithConfigure = &DbfsFilePathsDataSource{}

type DbfsFilePathsDataSource struct {
	Client *common.DatabricksClient
}

type DbfsFilePathInfo_SdkV2 struct {
	Path     types.String `tfsdk:"path"`
	FileSize types.Int64  `tfsdk:"file_size"`
}

func (DbfsFilePathInfo_SdkV2) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["path"] = attrs["path"].SetOptional()
	attrs["file_size"] = attrs["file_size"].SetOptional()
	return attrs
}

func (DbfsFilePathInfo_SdkV2) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{}
}

type DbfsFilePathsData_SdkV2 struct {
	Path      types.String `tfsdk:"path"`
	Recursive types.Bool   `tfsdk:"recursive"`
	PathList  types.Set    `tfsdk:"path_list"`
	tfschema.Namespace_SdkV2
}

func (DbfsFilePathsData_SdkV2) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["path"] = attrs["path"].SetRequired()
	attrs["recursive"] = attrs["recursive"].SetRequired()
	attrs["path_list"] = attrs["path_list"].SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (DbfsFilePathsData_SdkV2) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"path_list":       reflect.TypeOf(DbfsFilePathInfo_SdkV2{}),
		"provider_config": reflect.TypeOf(tfschema.ProviderConfig{}),
	}
}

func (d *DbfsFilePathsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceName)
}

func (d *DbfsFilePathsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, DbfsFilePathsData_SdkV2{}, func(c tfschema.CustomizableSchema) tfschema.CustomizableSchema {
		c.ConfigureAsSdkV2Compatible()
		c.AddValidator(listvalidator.SizeAtMost(1), "provider_config")
		return c
	})
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *DbfsFilePathsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *DbfsFilePathsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceName)

	var data DbfsFilePathsData_SdkV2
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

	path := data.Path.ValueString()
	recursive := data.Recursive.ValueBool()

	paths, err := storage.NewDbfsAPI(ctx, d.Client).List(path, recursive)
	if err != nil {
		resp.Diagnostics.AddError("failed to list DBFS paths", err.Error())
		return
	}

	pathList := make([]DbfsFilePathInfo_SdkV2, 0, len(paths))
	for _, pathInfo := range paths {
		pathList = append(pathList, DbfsFilePathInfo_SdkV2{
			Path:     types.StringValue(pathInfo.Path),
			FileSize: types.Int64Value(pathInfo.FileSize),
		})
	}

	pathListSet, diags := types.SetValueFrom(ctx, types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"path":      types.StringType,
			"file_size": types.Int64Type,
		},
	}, pathList)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.PathList = pathListSet

	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)

	_ = w
}
