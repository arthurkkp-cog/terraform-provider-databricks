package storage

import (
	"context"
	"encoding/base64"
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

const dataSourceName = "dbfs_file"

func DataSourceDbfsFile() datasource.DataSource {
	return &DbfsFileDataSource{}
}

var _ datasource.DataSourceWithConfigure = &DbfsFileDataSource{}

type DbfsFileDataSource struct {
	Client *common.DatabricksClient
}

type DbfsFileData struct {
	Path          types.String `tfsdk:"path"`
	LimitFileSize types.Bool   `tfsdk:"limit_file_size"`
	Content       types.String `tfsdk:"content"`
	FileSize      types.Int64  `tfsdk:"file_size"`
	tfschema.Namespace
}

func (DbfsFileData) ApplySchemaCustomizations(attrs map[string]tfschema.AttributeBuilder) map[string]tfschema.AttributeBuilder {
	attrs["path"] = attrs["path"].SetRequired()
	attrs["limit_file_size"] = attrs["limit_file_size"].SetRequired()
	attrs["content"] = attrs["content"].SetComputed()
	attrs["file_size"] = attrs["file_size"].SetComputed()
	attrs["provider_config"] = attrs["provider_config"].SetOptional()
	return attrs
}

func (DbfsFileData) GetComplexFieldTypes(context.Context) map[string]reflect.Type {
	return map[string]reflect.Type{
		"provider_config": reflect.TypeOf(tfschema.ProviderConfigData{}),
	}
}

func (d *DbfsFileDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = pluginfwcommon.GetDatabricksProductionName(dataSourceName)
}

func (d *DbfsFileDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs, blocks := tfschema.DataSourceStructToSchemaMap(ctx, DbfsFileData{}, nil)
	resp.Schema = schema.Schema{
		Attributes: attrs,
		Blocks:     blocks,
	}
}

func (d *DbfsFileDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if d.Client == nil {
		d.Client = pluginfwcommon.ConfigureDataSource(req, resp)
	}
}

func (d *DbfsFileDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = pluginfwcontext.SetUserAgentInDataSourceContext(ctx, dataSourceName)

	var data DbfsFileData
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

	path := data.Path.ValueString()
	limitFileSize := data.LimitFileSize.ValueBool()

	fileInfo, err := w.Dbfs.GetStatusByPath(ctx, path)
	if err != nil {
		resp.Diagnostics.AddError(fmt.Sprintf("failed to get status of %s", path), err.Error())
		return
	}

	if limitFileSize && fileInfo.FileSize > 4e6 {
		resp.Diagnostics.AddError(
			fmt.Sprintf("size of %s is too large", fileInfo.Path),
			fmt.Sprintf("file size is %d bytes, which exceeds the limit of 4MB", fileInfo.FileSize),
		)
		return
	}

	content, err := w.Dbfs.ReadFile(ctx, fileInfo.Path)
	if err != nil {
		resp.Diagnostics.AddError(fmt.Sprintf("failed to read %s", fileInfo.Path), err.Error())
		return
	}

	data.Path = types.StringValue(fileInfo.Path)
	data.FileSize = types.Int64Value(fileInfo.FileSize)
	data.Content = types.StringValue(base64.StdEncoding.EncodeToString(content))

	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
