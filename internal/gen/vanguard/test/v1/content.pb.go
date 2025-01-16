// Copyright 2023-2025 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.3
// 	protoc        (unknown)
// source: vanguard/test/v1/content.proto

package testv1

import (
	_ "google.golang.org/genproto/googleapis/api/annotations"
	httpbody "google.golang.org/genproto/googleapis/api/httpbody"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type IndexRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The path to the page to index.
	Page          string `protobuf:"bytes,1,opt,name=page,proto3" json:"page,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *IndexRequest) Reset() {
	*x = IndexRequest{}
	mi := &file_vanguard_test_v1_content_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *IndexRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*IndexRequest) ProtoMessage() {}

func (x *IndexRequest) ProtoReflect() protoreflect.Message {
	mi := &file_vanguard_test_v1_content_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use IndexRequest.ProtoReflect.Descriptor instead.
func (*IndexRequest) Descriptor() ([]byte, []int) {
	return file_vanguard_test_v1_content_proto_rawDescGZIP(), []int{0}
}

func (x *IndexRequest) GetPage() string {
	if x != nil {
		return x.Page
	}
	return ""
}

type UploadRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The path to the file to upload.
	Filename string `protobuf:"bytes,1,opt,name=filename,proto3" json:"filename,omitempty"`
	// The file contents to upload.
	File          *httpbody.HttpBody `protobuf:"bytes,2,opt,name=file,proto3" json:"file,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UploadRequest) Reset() {
	*x = UploadRequest{}
	mi := &file_vanguard_test_v1_content_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UploadRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UploadRequest) ProtoMessage() {}

func (x *UploadRequest) ProtoReflect() protoreflect.Message {
	mi := &file_vanguard_test_v1_content_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UploadRequest.ProtoReflect.Descriptor instead.
func (*UploadRequest) Descriptor() ([]byte, []int) {
	return file_vanguard_test_v1_content_proto_rawDescGZIP(), []int{1}
}

func (x *UploadRequest) GetFilename() string {
	if x != nil {
		return x.Filename
	}
	return ""
}

func (x *UploadRequest) GetFile() *httpbody.HttpBody {
	if x != nil {
		return x.File
	}
	return nil
}

type DownloadRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The path to the file to download.
	Filename      string `protobuf:"bytes,1,opt,name=filename,proto3" json:"filename,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DownloadRequest) Reset() {
	*x = DownloadRequest{}
	mi := &file_vanguard_test_v1_content_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DownloadRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DownloadRequest) ProtoMessage() {}

func (x *DownloadRequest) ProtoReflect() protoreflect.Message {
	mi := &file_vanguard_test_v1_content_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DownloadRequest.ProtoReflect.Descriptor instead.
func (*DownloadRequest) Descriptor() ([]byte, []int) {
	return file_vanguard_test_v1_content_proto_rawDescGZIP(), []int{2}
}

func (x *DownloadRequest) GetFilename() string {
	if x != nil {
		return x.Filename
	}
	return ""
}

type DownloadResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The file contents.
	File          *httpbody.HttpBody `protobuf:"bytes,1,opt,name=file,proto3" json:"file,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DownloadResponse) Reset() {
	*x = DownloadResponse{}
	mi := &file_vanguard_test_v1_content_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DownloadResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DownloadResponse) ProtoMessage() {}

func (x *DownloadResponse) ProtoReflect() protoreflect.Message {
	mi := &file_vanguard_test_v1_content_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DownloadResponse.ProtoReflect.Descriptor instead.
func (*DownloadResponse) Descriptor() ([]byte, []int) {
	return file_vanguard_test_v1_content_proto_rawDescGZIP(), []int{3}
}

func (x *DownloadResponse) GetFile() *httpbody.HttpBody {
	if x != nil {
		return x.File
	}
	return nil
}

type SubscribeRequest struct {
	state            protoimpl.MessageState `protogen:"open.v1"`
	FilenamePatterns []string               `protobuf:"bytes,1,rep,name=filename_patterns,json=filenamePatterns,proto3" json:"filename_patterns,omitempty"`
	unknownFields    protoimpl.UnknownFields
	sizeCache        protoimpl.SizeCache
}

func (x *SubscribeRequest) Reset() {
	*x = SubscribeRequest{}
	mi := &file_vanguard_test_v1_content_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SubscribeRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SubscribeRequest) ProtoMessage() {}

func (x *SubscribeRequest) ProtoReflect() protoreflect.Message {
	mi := &file_vanguard_test_v1_content_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SubscribeRequest.ProtoReflect.Descriptor instead.
func (*SubscribeRequest) Descriptor() ([]byte, []int) {
	return file_vanguard_test_v1_content_proto_rawDescGZIP(), []int{4}
}

func (x *SubscribeRequest) GetFilenamePatterns() []string {
	if x != nil {
		return x.FilenamePatterns
	}
	return nil
}

type SubscribeResponse struct {
	state           protoimpl.MessageState `protogen:"open.v1"`
	FilenameChanged string                 `protobuf:"bytes,1,opt,name=filename_changed,json=filenameChanged,proto3" json:"filename_changed,omitempty"`
	UpdateTime      *timestamppb.Timestamp `protobuf:"bytes,2,opt,name=update_time,json=updateTime,proto3" json:"update_time,omitempty"`
	Deleted         bool                   `protobuf:"varint,3,opt,name=deleted,proto3" json:"deleted,omitempty"`
	unknownFields   protoimpl.UnknownFields
	sizeCache       protoimpl.SizeCache
}

func (x *SubscribeResponse) Reset() {
	*x = SubscribeResponse{}
	mi := &file_vanguard_test_v1_content_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SubscribeResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SubscribeResponse) ProtoMessage() {}

func (x *SubscribeResponse) ProtoReflect() protoreflect.Message {
	mi := &file_vanguard_test_v1_content_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SubscribeResponse.ProtoReflect.Descriptor instead.
func (*SubscribeResponse) Descriptor() ([]byte, []int) {
	return file_vanguard_test_v1_content_proto_rawDescGZIP(), []int{5}
}

func (x *SubscribeResponse) GetFilenameChanged() string {
	if x != nil {
		return x.FilenameChanged
	}
	return ""
}

func (x *SubscribeResponse) GetUpdateTime() *timestamppb.Timestamp {
	if x != nil {
		return x.UpdateTime
	}
	return nil
}

func (x *SubscribeResponse) GetDeleted() bool {
	if x != nil {
		return x.Deleted
	}
	return false
}

var File_vanguard_test_v1_content_proto protoreflect.FileDescriptor

var file_vanguard_test_v1_content_proto_rawDesc = []byte{
	0x0a, 0x1e, 0x76, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x72, 0x64, 0x2f, 0x74, 0x65, 0x73, 0x74, 0x2f,
	0x76, 0x31, 0x2f, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x12, 0x10, 0x76, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x72, 0x64, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x2e,
	0x76, 0x31, 0x1a, 0x1c, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x61, 0x70, 0x69, 0x2f, 0x61,
	0x6e, 0x6e, 0x6f, 0x74, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x1a, 0x19, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x61, 0x70, 0x69, 0x2f, 0x68, 0x74, 0x74,
	0x70, 0x62, 0x6f, 0x64, 0x79, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x1b, 0x67, 0x6f, 0x6f,
	0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x65, 0x6d, 0x70,
	0x74, 0x79, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x1f, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65,
	0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74,
	0x61, 0x6d, 0x70, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x22, 0x0a, 0x0c, 0x49, 0x6e, 0x64,
	0x65, 0x78, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x70, 0x61, 0x67,
	0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x70, 0x61, 0x67, 0x65, 0x22, 0x55, 0x0a,
	0x0d, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x1a,
	0x0a, 0x08, 0x66, 0x69, 0x6c, 0x65, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x08, 0x66, 0x69, 0x6c, 0x65, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x28, 0x0a, 0x04, 0x66, 0x69,
	0x6c, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c,
	0x65, 0x2e, 0x61, 0x70, 0x69, 0x2e, 0x48, 0x74, 0x74, 0x70, 0x42, 0x6f, 0x64, 0x79, 0x52, 0x04,
	0x66, 0x69, 0x6c, 0x65, 0x22, 0x2d, 0x0a, 0x0f, 0x44, 0x6f, 0x77, 0x6e, 0x6c, 0x6f, 0x61, 0x64,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x1a, 0x0a, 0x08, 0x66, 0x69, 0x6c, 0x65, 0x6e,
	0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x66, 0x69, 0x6c, 0x65, 0x6e,
	0x61, 0x6d, 0x65, 0x22, 0x3c, 0x0a, 0x10, 0x44, 0x6f, 0x77, 0x6e, 0x6c, 0x6f, 0x61, 0x64, 0x52,
	0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x28, 0x0a, 0x04, 0x66, 0x69, 0x6c, 0x65, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x61,
	0x70, 0x69, 0x2e, 0x48, 0x74, 0x74, 0x70, 0x42, 0x6f, 0x64, 0x79, 0x52, 0x04, 0x66, 0x69, 0x6c,
	0x65, 0x22, 0x3f, 0x0a, 0x10, 0x53, 0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62, 0x65, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x2b, 0x0a, 0x11, 0x66, 0x69, 0x6c, 0x65, 0x6e, 0x61, 0x6d,
	0x65, 0x5f, 0x70, 0x61, 0x74, 0x74, 0x65, 0x72, 0x6e, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x09,
	0x52, 0x10, 0x66, 0x69, 0x6c, 0x65, 0x6e, 0x61, 0x6d, 0x65, 0x50, 0x61, 0x74, 0x74, 0x65, 0x72,
	0x6e, 0x73, 0x22, 0x95, 0x01, 0x0a, 0x11, 0x53, 0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62, 0x65,
	0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x29, 0x0a, 0x10, 0x66, 0x69, 0x6c, 0x65,
	0x6e, 0x61, 0x6d, 0x65, 0x5f, 0x63, 0x68, 0x61, 0x6e, 0x67, 0x65, 0x64, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0f, 0x66, 0x69, 0x6c, 0x65, 0x6e, 0x61, 0x6d, 0x65, 0x43, 0x68, 0x61, 0x6e,
	0x67, 0x65, 0x64, 0x12, 0x3b, 0x0a, 0x0b, 0x75, 0x70, 0x64, 0x61, 0x74, 0x65, 0x5f, 0x74, 0x69,
	0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c,
	0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73,
	0x74, 0x61, 0x6d, 0x70, 0x52, 0x0a, 0x75, 0x70, 0x64, 0x61, 0x74, 0x65, 0x54, 0x69, 0x6d, 0x65,
	0x12, 0x18, 0x0a, 0x07, 0x64, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x64, 0x18, 0x03, 0x20, 0x01, 0x28,
	0x08, 0x52, 0x07, 0x64, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x64, 0x32, 0xa3, 0x03, 0x0a, 0x0e, 0x43,
	0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x51, 0x0a,
	0x05, 0x49, 0x6e, 0x64, 0x65, 0x78, 0x12, 0x1e, 0x2e, 0x76, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x72,
	0x64, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x49, 0x6e, 0x64, 0x65, 0x78, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x14, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e,
	0x61, 0x70, 0x69, 0x2e, 0x48, 0x74, 0x74, 0x70, 0x42, 0x6f, 0x64, 0x79, 0x22, 0x12, 0x82, 0xd3,
	0xe4, 0x93, 0x02, 0x0c, 0x12, 0x0a, 0x2f, 0x7b, 0x70, 0x61, 0x67, 0x65, 0x3d, 0x2a, 0x2a, 0x7d,
	0x12, 0x68, 0x0a, 0x06, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x12, 0x1f, 0x2e, 0x76, 0x61, 0x6e,
	0x67, 0x75, 0x61, 0x72, 0x64, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x55, 0x70,
	0x6c, 0x6f, 0x61, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x16, 0x2e, 0x67, 0x6f,
	0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x45, 0x6d,
	0x70, 0x74, 0x79, 0x22, 0x23, 0x82, 0xd3, 0xe4, 0x93, 0x02, 0x1d, 0x3a, 0x04, 0x66, 0x69, 0x6c,
	0x65, 0x22, 0x15, 0x2f, 0x7b, 0x66, 0x69, 0x6c, 0x65, 0x6e, 0x61, 0x6d, 0x65, 0x3d, 0x2a, 0x2a,
	0x7d, 0x3a, 0x75, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x28, 0x01, 0x12, 0x7a, 0x0a, 0x08, 0x44, 0x6f,
	0x77, 0x6e, 0x6c, 0x6f, 0x61, 0x64, 0x12, 0x21, 0x2e, 0x76, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x72,
	0x64, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x44, 0x6f, 0x77, 0x6e, 0x6c, 0x6f,
	0x61, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x22, 0x2e, 0x76, 0x61, 0x6e, 0x67,
	0x75, 0x61, 0x72, 0x64, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x44, 0x6f, 0x77,
	0x6e, 0x6c, 0x6f, 0x61, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x25, 0x82,
	0xd3, 0xe4, 0x93, 0x02, 0x1f, 0x62, 0x04, 0x66, 0x69, 0x6c, 0x65, 0x12, 0x17, 0x2f, 0x7b, 0x66,
	0x69, 0x6c, 0x65, 0x6e, 0x61, 0x6d, 0x65, 0x3d, 0x2a, 0x2a, 0x7d, 0x3a, 0x64, 0x6f, 0x77, 0x6e,
	0x6c, 0x6f, 0x61, 0x64, 0x30, 0x01, 0x12, 0x58, 0x0a, 0x09, 0x53, 0x75, 0x62, 0x73, 0x63, 0x72,
	0x69, 0x62, 0x65, 0x12, 0x22, 0x2e, 0x76, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x72, 0x64, 0x2e, 0x74,
	0x65, 0x73, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x53, 0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62, 0x65,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x23, 0x2e, 0x76, 0x61, 0x6e, 0x67, 0x75, 0x61,
	0x72, 0x64, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x53, 0x75, 0x62, 0x73, 0x63,
	0x72, 0x69, 0x62, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x28, 0x01, 0x30, 0x01,
	0x42, 0xc4, 0x01, 0x0a, 0x14, 0x63, 0x6f, 0x6d, 0x2e, 0x76, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x72,
	0x64, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x2e, 0x76, 0x31, 0x42, 0x0c, 0x43, 0x6f, 0x6e, 0x74, 0x65,
	0x6e, 0x74, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x3c, 0x63, 0x6f, 0x6e, 0x6e, 0x65,
	0x63, 0x74, 0x72, 0x70, 0x63, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x76, 0x61, 0x6e, 0x67, 0x75, 0x61,
	0x72, 0x64, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x67, 0x65, 0x6e, 0x2f,
	0x76, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x72, 0x64, 0x2f, 0x74, 0x65, 0x73, 0x74, 0x2f, 0x76, 0x31,
	0x3b, 0x74, 0x65, 0x73, 0x74, 0x76, 0x31, 0xa2, 0x02, 0x03, 0x56, 0x54, 0x58, 0xaa, 0x02, 0x10,
	0x56, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x72, 0x64, 0x2e, 0x54, 0x65, 0x73, 0x74, 0x2e, 0x56, 0x31,
	0xca, 0x02, 0x10, 0x56, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x72, 0x64, 0x5c, 0x54, 0x65, 0x73, 0x74,
	0x5c, 0x56, 0x31, 0xe2, 0x02, 0x1c, 0x56, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x72, 0x64, 0x5c, 0x54,
	0x65, 0x73, 0x74, 0x5c, 0x56, 0x31, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61,
	0x74, 0x61, 0xea, 0x02, 0x12, 0x56, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x72, 0x64, 0x3a, 0x3a, 0x54,
	0x65, 0x73, 0x74, 0x3a, 0x3a, 0x56, 0x31, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_vanguard_test_v1_content_proto_rawDescOnce sync.Once
	file_vanguard_test_v1_content_proto_rawDescData = file_vanguard_test_v1_content_proto_rawDesc
)

func file_vanguard_test_v1_content_proto_rawDescGZIP() []byte {
	file_vanguard_test_v1_content_proto_rawDescOnce.Do(func() {
		file_vanguard_test_v1_content_proto_rawDescData = protoimpl.X.CompressGZIP(file_vanguard_test_v1_content_proto_rawDescData)
	})
	return file_vanguard_test_v1_content_proto_rawDescData
}

var file_vanguard_test_v1_content_proto_msgTypes = make([]protoimpl.MessageInfo, 6)
var file_vanguard_test_v1_content_proto_goTypes = []any{
	(*IndexRequest)(nil),          // 0: vanguard.test.v1.IndexRequest
	(*UploadRequest)(nil),         // 1: vanguard.test.v1.UploadRequest
	(*DownloadRequest)(nil),       // 2: vanguard.test.v1.DownloadRequest
	(*DownloadResponse)(nil),      // 3: vanguard.test.v1.DownloadResponse
	(*SubscribeRequest)(nil),      // 4: vanguard.test.v1.SubscribeRequest
	(*SubscribeResponse)(nil),     // 5: vanguard.test.v1.SubscribeResponse
	(*httpbody.HttpBody)(nil),     // 6: google.api.HttpBody
	(*timestamppb.Timestamp)(nil), // 7: google.protobuf.Timestamp
	(*emptypb.Empty)(nil),         // 8: google.protobuf.Empty
}
var file_vanguard_test_v1_content_proto_depIdxs = []int32{
	6, // 0: vanguard.test.v1.UploadRequest.file:type_name -> google.api.HttpBody
	6, // 1: vanguard.test.v1.DownloadResponse.file:type_name -> google.api.HttpBody
	7, // 2: vanguard.test.v1.SubscribeResponse.update_time:type_name -> google.protobuf.Timestamp
	0, // 3: vanguard.test.v1.ContentService.Index:input_type -> vanguard.test.v1.IndexRequest
	1, // 4: vanguard.test.v1.ContentService.Upload:input_type -> vanguard.test.v1.UploadRequest
	2, // 5: vanguard.test.v1.ContentService.Download:input_type -> vanguard.test.v1.DownloadRequest
	4, // 6: vanguard.test.v1.ContentService.Subscribe:input_type -> vanguard.test.v1.SubscribeRequest
	6, // 7: vanguard.test.v1.ContentService.Index:output_type -> google.api.HttpBody
	8, // 8: vanguard.test.v1.ContentService.Upload:output_type -> google.protobuf.Empty
	3, // 9: vanguard.test.v1.ContentService.Download:output_type -> vanguard.test.v1.DownloadResponse
	5, // 10: vanguard.test.v1.ContentService.Subscribe:output_type -> vanguard.test.v1.SubscribeResponse
	7, // [7:11] is the sub-list for method output_type
	3, // [3:7] is the sub-list for method input_type
	3, // [3:3] is the sub-list for extension type_name
	3, // [3:3] is the sub-list for extension extendee
	0, // [0:3] is the sub-list for field type_name
}

func init() { file_vanguard_test_v1_content_proto_init() }
func file_vanguard_test_v1_content_proto_init() {
	if File_vanguard_test_v1_content_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_vanguard_test_v1_content_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   6,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_vanguard_test_v1_content_proto_goTypes,
		DependencyIndexes: file_vanguard_test_v1_content_proto_depIdxs,
		MessageInfos:      file_vanguard_test_v1_content_proto_msgTypes,
	}.Build()
	File_vanguard_test_v1_content_proto = out.File
	file_vanguard_test_v1_content_proto_rawDesc = nil
	file_vanguard_test_v1_content_proto_goTypes = nil
	file_vanguard_test_v1_content_proto_depIdxs = nil
}
