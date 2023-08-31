// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.31.0
// 	protoc        (unknown)
// source: io/swagger/petstore/v2/pets.proto

package petstorev2

import (
	_ "google.golang.org/genproto/googleapis/api/annotations"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type Status int32

const (
	Status_available Status = 0
	Status_pending   Status = 1
	Status_sold      Status = 2
)

// Enum value maps for Status.
var (
	Status_name = map[int32]string{
		0: "available",
		1: "pending",
		2: "sold",
	}
	Status_value = map[string]int32{
		"available": 0,
		"pending":   1,
		"sold":      2,
	}
)

func (x Status) Enum() *Status {
	p := new(Status)
	*p = x
	return p
}

func (x Status) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (Status) Descriptor() protoreflect.EnumDescriptor {
	return file_io_swagger_petstore_v2_pets_proto_enumTypes[0].Descriptor()
}

func (Status) Type() protoreflect.EnumType {
	return &file_io_swagger_petstore_v2_pets_proto_enumTypes[0]
}

func (x Status) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use Status.Descriptor instead.
func (Status) EnumDescriptor() ([]byte, []int) {
	return file_io_swagger_petstore_v2_pets_proto_rawDescGZIP(), []int{0}
}

type Category struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id   int64  `protobuf:"varint,1,opt,name=id,proto3" json:"id,omitempty"`
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
}

func (x *Category) Reset() {
	*x = Category{}
	if protoimpl.UnsafeEnabled {
		mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Category) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Category) ProtoMessage() {}

func (x *Category) ProtoReflect() protoreflect.Message {
	mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Category.ProtoReflect.Descriptor instead.
func (*Category) Descriptor() ([]byte, []int) {
	return file_io_swagger_petstore_v2_pets_proto_rawDescGZIP(), []int{0}
}

func (x *Category) GetId() int64 {
	if x != nil {
		return x.Id
	}
	return 0
}

func (x *Category) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

type Tag struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id   int64  `protobuf:"varint,1,opt,name=id,proto3" json:"id,omitempty"`
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
}

func (x *Tag) Reset() {
	*x = Tag{}
	if protoimpl.UnsafeEnabled {
		mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Tag) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Tag) ProtoMessage() {}

func (x *Tag) ProtoReflect() protoreflect.Message {
	mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Tag.ProtoReflect.Descriptor instead.
func (*Tag) Descriptor() ([]byte, []int) {
	return file_io_swagger_petstore_v2_pets_proto_rawDescGZIP(), []int{1}
}

func (x *Tag) GetId() int64 {
	if x != nil {
		return x.Id
	}
	return 0
}

func (x *Tag) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

type PetID struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	PetId int64 `protobuf:"varint,1,opt,name=pet_id,json=petId,proto3" json:"pet_id,omitempty"`
}

func (x *PetID) Reset() {
	*x = PetID{}
	if protoimpl.UnsafeEnabled {
		mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PetID) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PetID) ProtoMessage() {}

func (x *PetID) ProtoReflect() protoreflect.Message {
	mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PetID.ProtoReflect.Descriptor instead.
func (*PetID) Descriptor() ([]byte, []int) {
	return file_io_swagger_petstore_v2_pets_proto_rawDescGZIP(), []int{2}
}

func (x *PetID) GetPetId() int64 {
	if x != nil {
		return x.PetId
	}
	return 0
}

type Pet struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Id        int64     `protobuf:"varint,1,opt,name=id,proto3" json:"id,omitempty"`
	Category  *Category `protobuf:"bytes,2,opt,name=category,proto3" json:"category,omitempty"`
	Name      string    `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`
	PhotoUrls []string  `protobuf:"bytes,4,rep,name=photo_urls,json=photoUrls,proto3" json:"photo_urls,omitempty"`
	Tags      []*Tag    `protobuf:"bytes,5,rep,name=tags,proto3" json:"tags,omitempty"`
	Status    string    `protobuf:"bytes,6,opt,name=status,proto3" json:"status,omitempty"`
}

func (x *Pet) Reset() {
	*x = Pet{}
	if protoimpl.UnsafeEnabled {
		mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Pet) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Pet) ProtoMessage() {}

func (x *Pet) ProtoReflect() protoreflect.Message {
	mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Pet.ProtoReflect.Descriptor instead.
func (*Pet) Descriptor() ([]byte, []int) {
	return file_io_swagger_petstore_v2_pets_proto_rawDescGZIP(), []int{3}
}

func (x *Pet) GetId() int64 {
	if x != nil {
		return x.Id
	}
	return 0
}

func (x *Pet) GetCategory() *Category {
	if x != nil {
		return x.Category
	}
	return nil
}

func (x *Pet) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Pet) GetPhotoUrls() []string {
	if x != nil {
		return x.PhotoUrls
	}
	return nil
}

func (x *Pet) GetTags() []*Tag {
	if x != nil {
		return x.Tags
	}
	return nil
}

func (x *Pet) GetStatus() string {
	if x != nil {
		return x.Status
	}
	return ""
}

type UpdatePetWithFormReq struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	PetId  int64  `protobuf:"varint,1,opt,name=pet_id,json=petId,proto3" json:"pet_id,omitempty"`
	Name   string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Status string `protobuf:"bytes,3,opt,name=status,proto3" json:"status,omitempty"`
}

func (x *UpdatePetWithFormReq) Reset() {
	*x = UpdatePetWithFormReq{}
	if protoimpl.UnsafeEnabled {
		mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *UpdatePetWithFormReq) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdatePetWithFormReq) ProtoMessage() {}

func (x *UpdatePetWithFormReq) ProtoReflect() protoreflect.Message {
	mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdatePetWithFormReq.ProtoReflect.Descriptor instead.
func (*UpdatePetWithFormReq) Descriptor() ([]byte, []int) {
	return file_io_swagger_petstore_v2_pets_proto_rawDescGZIP(), []int{4}
}

func (x *UpdatePetWithFormReq) GetPetId() int64 {
	if x != nil {
		return x.PetId
	}
	return 0
}

func (x *UpdatePetWithFormReq) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *UpdatePetWithFormReq) GetStatus() string {
	if x != nil {
		return x.Status
	}
	return ""
}

type UploadFileReq struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	PetId              int64  `protobuf:"varint,1,opt,name=pet_id,json=petId,proto3" json:"pet_id,omitempty"`
	AdditionalMetadata string `protobuf:"bytes,2,opt,name=additional_metadata,json=additionalMetadata,proto3" json:"additional_metadata,omitempty"`
	File               string `protobuf:"bytes,3,opt,name=file,proto3" json:"file,omitempty"`
}

func (x *UploadFileReq) Reset() {
	*x = UploadFileReq{}
	if protoimpl.UnsafeEnabled {
		mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *UploadFileReq) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UploadFileReq) ProtoMessage() {}

func (x *UploadFileReq) ProtoReflect() protoreflect.Message {
	mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UploadFileReq.ProtoReflect.Descriptor instead.
func (*UploadFileReq) Descriptor() ([]byte, []int) {
	return file_io_swagger_petstore_v2_pets_proto_rawDescGZIP(), []int{5}
}

func (x *UploadFileReq) GetPetId() int64 {
	if x != nil {
		return x.PetId
	}
	return 0
}

func (x *UploadFileReq) GetAdditionalMetadata() string {
	if x != nil {
		return x.AdditionalMetadata
	}
	return ""
}

func (x *UploadFileReq) GetFile() string {
	if x != nil {
		return x.File
	}
	return ""
}

type PetBody struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	PetId int64  `protobuf:"varint,1,opt,name=pet_id,json=petId,proto3" json:"pet_id,omitempty"`
	Body  string `protobuf:"bytes,2,opt,name=body,proto3" json:"body,omitempty"`
}

func (x *PetBody) Reset() {
	*x = PetBody{}
	if protoimpl.UnsafeEnabled {
		mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[6]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PetBody) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PetBody) ProtoMessage() {}

func (x *PetBody) ProtoReflect() protoreflect.Message {
	mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[6]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PetBody.ProtoReflect.Descriptor instead.
func (*PetBody) Descriptor() ([]byte, []int) {
	return file_io_swagger_petstore_v2_pets_proto_rawDescGZIP(), []int{6}
}

func (x *PetBody) GetPetId() int64 {
	if x != nil {
		return x.PetId
	}
	return 0
}

func (x *PetBody) GetBody() string {
	if x != nil {
		return x.Body
	}
	return ""
}

type StatusReq struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Status []string `protobuf:"bytes,1,rep,name=status,proto3" json:"status,omitempty"`
}

func (x *StatusReq) Reset() {
	*x = StatusReq{}
	if protoimpl.UnsafeEnabled {
		mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[7]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *StatusReq) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*StatusReq) ProtoMessage() {}

func (x *StatusReq) ProtoReflect() protoreflect.Message {
	mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[7]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use StatusReq.ProtoReflect.Descriptor instead.
func (*StatusReq) Descriptor() ([]byte, []int) {
	return file_io_swagger_petstore_v2_pets_proto_rawDescGZIP(), []int{7}
}

func (x *StatusReq) GetStatus() []string {
	if x != nil {
		return x.Status
	}
	return nil
}

type TagReq struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Tag []string `protobuf:"bytes,1,rep,name=tag,proto3" json:"tag,omitempty"`
}

func (x *TagReq) Reset() {
	*x = TagReq{}
	if protoimpl.UnsafeEnabled {
		mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[8]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TagReq) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TagReq) ProtoMessage() {}

func (x *TagReq) ProtoReflect() protoreflect.Message {
	mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[8]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TagReq.ProtoReflect.Descriptor instead.
func (*TagReq) Descriptor() ([]byte, []int) {
	return file_io_swagger_petstore_v2_pets_proto_rawDescGZIP(), []int{8}
}

func (x *TagReq) GetTag() []string {
	if x != nil {
		return x.Tag
	}
	return nil
}

type Pets struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Pets []*Pet `protobuf:"bytes,1,rep,name=pets,proto3" json:"pets,omitempty"`
}

func (x *Pets) Reset() {
	*x = Pets{}
	if protoimpl.UnsafeEnabled {
		mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[9]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Pets) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Pets) ProtoMessage() {}

func (x *Pets) ProtoReflect() protoreflect.Message {
	mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[9]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Pets.ProtoReflect.Descriptor instead.
func (*Pets) Descriptor() ([]byte, []int) {
	return file_io_swagger_petstore_v2_pets_proto_rawDescGZIP(), []int{9}
}

func (x *Pets) GetPets() []*Pet {
	if x != nil {
		return x.Pets
	}
	return nil
}

type ApiResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Code    int32  `protobuf:"varint,1,opt,name=code,proto3" json:"code,omitempty"`
	Type    string `protobuf:"bytes,2,opt,name=type,proto3" json:"type,omitempty"`
	Message string `protobuf:"bytes,3,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *ApiResponse) Reset() {
	*x = ApiResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[10]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ApiResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ApiResponse) ProtoMessage() {}

func (x *ApiResponse) ProtoReflect() protoreflect.Message {
	mi := &file_io_swagger_petstore_v2_pets_proto_msgTypes[10]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ApiResponse.ProtoReflect.Descriptor instead.
func (*ApiResponse) Descriptor() ([]byte, []int) {
	return file_io_swagger_petstore_v2_pets_proto_rawDescGZIP(), []int{10}
}

func (x *ApiResponse) GetCode() int32 {
	if x != nil {
		return x.Code
	}
	return 0
}

func (x *ApiResponse) GetType() string {
	if x != nil {
		return x.Type
	}
	return ""
}

func (x *ApiResponse) GetMessage() string {
	if x != nil {
		return x.Message
	}
	return ""
}

var File_io_swagger_petstore_v2_pets_proto protoreflect.FileDescriptor

var file_io_swagger_petstore_v2_pets_proto_rawDesc = []byte{
	0x0a, 0x21, 0x69, 0x6f, 0x2f, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2f, 0x70, 0x65, 0x74,
	0x73, 0x74, 0x6f, 0x72, 0x65, 0x2f, 0x76, 0x32, 0x2f, 0x70, 0x65, 0x74, 0x73, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x12, 0x16, 0x69, 0x6f, 0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e,
	0x70, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2e, 0x76, 0x32, 0x1a, 0x1c, 0x67, 0x6f, 0x6f,
	0x67, 0x6c, 0x65, 0x2f, 0x61, 0x70, 0x69, 0x2f, 0x61, 0x6e, 0x6e, 0x6f, 0x74, 0x61, 0x74, 0x69,
	0x6f, 0x6e, 0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x1b, 0x67, 0x6f, 0x6f, 0x67, 0x6c,
	0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x65, 0x6d, 0x70, 0x74, 0x79,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x2e, 0x0a, 0x08, 0x43, 0x61, 0x74, 0x65, 0x67, 0x6f,
	0x72, 0x79, 0x12, 0x0e, 0x0a, 0x02, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x02,
	0x69, 0x64, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x22, 0x29, 0x0a, 0x03, 0x54, 0x61, 0x67, 0x12, 0x0e, 0x0a,
	0x02, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x02, 0x69, 0x64, 0x12, 0x12, 0x0a,
	0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d,
	0x65, 0x22, 0x1e, 0x0a, 0x05, 0x50, 0x65, 0x74, 0x49, 0x44, 0x12, 0x15, 0x0a, 0x06, 0x70, 0x65,
	0x74, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x05, 0x70, 0x65, 0x74, 0x49,
	0x64, 0x22, 0xcf, 0x01, 0x0a, 0x03, 0x50, 0x65, 0x74, 0x12, 0x0e, 0x0a, 0x02, 0x69, 0x64, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x02, 0x69, 0x64, 0x12, 0x3c, 0x0a, 0x08, 0x63, 0x61, 0x74,
	0x65, 0x67, 0x6f, 0x72, 0x79, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x20, 0x2e, 0x69, 0x6f,
	0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72,
	0x65, 0x2e, 0x76, 0x32, 0x2e, 0x43, 0x61, 0x74, 0x65, 0x67, 0x6f, 0x72, 0x79, 0x52, 0x08, 0x63,
	0x61, 0x74, 0x65, 0x67, 0x6f, 0x72, 0x79, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18,
	0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x1d, 0x0a, 0x0a, 0x70,
	0x68, 0x6f, 0x74, 0x6f, 0x5f, 0x75, 0x72, 0x6c, 0x73, 0x18, 0x04, 0x20, 0x03, 0x28, 0x09, 0x52,
	0x09, 0x70, 0x68, 0x6f, 0x74, 0x6f, 0x55, 0x72, 0x6c, 0x73, 0x12, 0x2f, 0x0a, 0x04, 0x74, 0x61,
	0x67, 0x73, 0x18, 0x05, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1b, 0x2e, 0x69, 0x6f, 0x2e, 0x73, 0x77,
	0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2e, 0x76,
	0x32, 0x2e, 0x54, 0x61, 0x67, 0x52, 0x04, 0x74, 0x61, 0x67, 0x73, 0x12, 0x16, 0x0a, 0x06, 0x73,
	0x74, 0x61, 0x74, 0x75, 0x73, 0x18, 0x06, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x73, 0x74, 0x61,
	0x74, 0x75, 0x73, 0x22, 0x59, 0x0a, 0x14, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x50, 0x65, 0x74,
	0x57, 0x69, 0x74, 0x68, 0x46, 0x6f, 0x72, 0x6d, 0x52, 0x65, 0x71, 0x12, 0x15, 0x0a, 0x06, 0x70,
	0x65, 0x74, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x05, 0x70, 0x65, 0x74,
	0x49, 0x64, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73,
	0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x22, 0x6b,
	0x0a, 0x0d, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x46, 0x69, 0x6c, 0x65, 0x52, 0x65, 0x71, 0x12,
	0x15, 0x0a, 0x06, 0x70, 0x65, 0x74, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x03, 0x52,
	0x05, 0x70, 0x65, 0x74, 0x49, 0x64, 0x12, 0x2f, 0x0a, 0x13, 0x61, 0x64, 0x64, 0x69, 0x74, 0x69,
	0x6f, 0x6e, 0x61, 0x6c, 0x5f, 0x6d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x12, 0x61, 0x64, 0x64, 0x69, 0x74, 0x69, 0x6f, 0x6e, 0x61, 0x6c, 0x4d,
	0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0x12, 0x12, 0x0a, 0x04, 0x66, 0x69, 0x6c, 0x65, 0x18,
	0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x66, 0x69, 0x6c, 0x65, 0x22, 0x34, 0x0a, 0x07, 0x50,
	0x65, 0x74, 0x42, 0x6f, 0x64, 0x79, 0x12, 0x15, 0x0a, 0x06, 0x70, 0x65, 0x74, 0x5f, 0x69, 0x64,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x05, 0x70, 0x65, 0x74, 0x49, 0x64, 0x12, 0x12, 0x0a,
	0x04, 0x62, 0x6f, 0x64, 0x79, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x62, 0x6f, 0x64,
	0x79, 0x22, 0x23, 0x0a, 0x09, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x52, 0x65, 0x71, 0x12, 0x16,
	0x0a, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x09, 0x52, 0x06,
	0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x22, 0x1a, 0x0a, 0x06, 0x54, 0x61, 0x67, 0x52, 0x65, 0x71,
	0x12, 0x10, 0x0a, 0x03, 0x74, 0x61, 0x67, 0x18, 0x01, 0x20, 0x03, 0x28, 0x09, 0x52, 0x03, 0x74,
	0x61, 0x67, 0x22, 0x37, 0x0a, 0x04, 0x50, 0x65, 0x74, 0x73, 0x12, 0x2f, 0x0a, 0x04, 0x70, 0x65,
	0x74, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1b, 0x2e, 0x69, 0x6f, 0x2e, 0x73, 0x77,
	0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2e, 0x76,
	0x32, 0x2e, 0x50, 0x65, 0x74, 0x52, 0x04, 0x70, 0x65, 0x74, 0x73, 0x22, 0x4f, 0x0a, 0x0b, 0x41,
	0x70, 0x69, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x63, 0x6f,
	0x64, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x05, 0x52, 0x04, 0x63, 0x6f, 0x64, 0x65, 0x12, 0x12,
	0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x74, 0x79,
	0x70, 0x65, 0x12, 0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x03, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x2a, 0x2e, 0x0a, 0x06,
	0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x12, 0x0d, 0x0a, 0x09, 0x61, 0x76, 0x61, 0x69, 0x6c, 0x61,
	0x62, 0x6c, 0x65, 0x10, 0x00, 0x12, 0x0b, 0x0a, 0x07, 0x70, 0x65, 0x6e, 0x64, 0x69, 0x6e, 0x67,
	0x10, 0x01, 0x12, 0x08, 0x0a, 0x04, 0x73, 0x6f, 0x6c, 0x64, 0x10, 0x02, 0x32, 0xcb, 0x06, 0x0a,
	0x0a, 0x50, 0x65, 0x74, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x62, 0x0a, 0x0a, 0x47,
	0x65, 0x74, 0x50, 0x65, 0x74, 0x42, 0x79, 0x49, 0x44, 0x12, 0x1d, 0x2e, 0x69, 0x6f, 0x2e, 0x73,
	0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2e,
	0x76, 0x32, 0x2e, 0x50, 0x65, 0x74, 0x49, 0x44, 0x1a, 0x1b, 0x2e, 0x69, 0x6f, 0x2e, 0x73, 0x77,
	0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2e, 0x76,
	0x32, 0x2e, 0x50, 0x65, 0x74, 0x22, 0x18, 0x82, 0xd3, 0xe4, 0x93, 0x02, 0x0f, 0x12, 0x0d, 0x2f,
	0x70, 0x65, 0x74, 0x2f, 0x7b, 0x70, 0x65, 0x74, 0x5f, 0x69, 0x64, 0x7d, 0x90, 0x02, 0x01, 0x12,
	0x73, 0x0a, 0x11, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x50, 0x65, 0x74, 0x57, 0x69, 0x74, 0x68,
	0x46, 0x6f, 0x72, 0x6d, 0x12, 0x2c, 0x2e, 0x69, 0x6f, 0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65,
	0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2e, 0x76, 0x32, 0x2e, 0x55, 0x70,
	0x64, 0x61, 0x74, 0x65, 0x50, 0x65, 0x74, 0x57, 0x69, 0x74, 0x68, 0x46, 0x6f, 0x72, 0x6d, 0x52,
	0x65, 0x71, 0x1a, 0x16, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x62, 0x75, 0x66, 0x2e, 0x45, 0x6d, 0x70, 0x74, 0x79, 0x22, 0x18, 0x82, 0xd3, 0xe4, 0x93,
	0x02, 0x12, 0x3a, 0x01, 0x2a, 0x22, 0x0d, 0x2f, 0x70, 0x65, 0x74, 0x2f, 0x7b, 0x70, 0x65, 0x74,
	0x5f, 0x69, 0x64, 0x7d, 0x12, 0x59, 0x0a, 0x09, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x50, 0x65,
	0x74, 0x12, 0x1d, 0x2e, 0x69, 0x6f, 0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70,
	0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2e, 0x76, 0x32, 0x2e, 0x50, 0x65, 0x74, 0x49, 0x44,
	0x1a, 0x16, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62,
	0x75, 0x66, 0x2e, 0x45, 0x6d, 0x70, 0x74, 0x79, 0x22, 0x15, 0x82, 0xd3, 0xe4, 0x93, 0x02, 0x0f,
	0x2a, 0x0d, 0x2f, 0x70, 0x65, 0x74, 0x2f, 0x7b, 0x70, 0x65, 0x74, 0x5f, 0x69, 0x64, 0x7d, 0x12,
	0x7e, 0x0a, 0x0a, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x46, 0x69, 0x6c, 0x65, 0x12, 0x25, 0x2e,
	0x69, 0x6f, 0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74,
	0x6f, 0x72, 0x65, 0x2e, 0x76, 0x32, 0x2e, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x46, 0x69, 0x6c,
	0x65, 0x52, 0x65, 0x71, 0x1a, 0x23, 0x2e, 0x69, 0x6f, 0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65,
	0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2e, 0x76, 0x32, 0x2e, 0x41, 0x70,
	0x69, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x24, 0x82, 0xd3, 0xe4, 0x93, 0x02,
	0x1e, 0x3a, 0x01, 0x2a, 0x22, 0x19, 0x2f, 0x70, 0x65, 0x74, 0x2f, 0x7b, 0x70, 0x65, 0x74, 0x5f,
	0x69, 0x64, 0x7d, 0x2f, 0x75, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x49, 0x6d, 0x61, 0x67, 0x65, 0x12,
	0x53, 0x0a, 0x06, 0x41, 0x64, 0x64, 0x50, 0x65, 0x74, 0x12, 0x1b, 0x2e, 0x69, 0x6f, 0x2e, 0x73,
	0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2e,
	0x76, 0x32, 0x2e, 0x50, 0x65, 0x74, 0x1a, 0x1b, 0x2e, 0x69, 0x6f, 0x2e, 0x73, 0x77, 0x61, 0x67,
	0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2e, 0x76, 0x32, 0x2e,
	0x50, 0x65, 0x74, 0x22, 0x0f, 0x82, 0xd3, 0xe4, 0x93, 0x02, 0x09, 0x3a, 0x01, 0x2a, 0x22, 0x04,
	0x2f, 0x70, 0x65, 0x74, 0x12, 0x56, 0x0a, 0x09, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x50, 0x65,
	0x74, 0x12, 0x1b, 0x2e, 0x69, 0x6f, 0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70,
	0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2e, 0x76, 0x32, 0x2e, 0x50, 0x65, 0x74, 0x1a, 0x1b,
	0x2e, 0x69, 0x6f, 0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73,
	0x74, 0x6f, 0x72, 0x65, 0x2e, 0x76, 0x32, 0x2e, 0x50, 0x65, 0x74, 0x22, 0x0f, 0x82, 0xd3, 0xe4,
	0x93, 0x02, 0x09, 0x3a, 0x01, 0x2a, 0x1a, 0x04, 0x2f, 0x70, 0x65, 0x74, 0x12, 0x69, 0x0a, 0x0d,
	0x46, 0x69, 0x6e, 0x64, 0x50, 0x65, 0x74, 0x73, 0x42, 0x79, 0x54, 0x61, 0x67, 0x12, 0x1e, 0x2e,
	0x69, 0x6f, 0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74,
	0x6f, 0x72, 0x65, 0x2e, 0x76, 0x32, 0x2e, 0x54, 0x61, 0x67, 0x52, 0x65, 0x71, 0x1a, 0x1c, 0x2e,
	0x69, 0x6f, 0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74,
	0x6f, 0x72, 0x65, 0x2e, 0x76, 0x32, 0x2e, 0x50, 0x65, 0x74, 0x73, 0x22, 0x1a, 0x82, 0xd3, 0xe4,
	0x93, 0x02, 0x11, 0x12, 0x0f, 0x2f, 0x70, 0x65, 0x74, 0x2f, 0x66, 0x69, 0x6e, 0x64, 0x42, 0x79,
	0x54, 0x61, 0x67, 0x73, 0x90, 0x02, 0x01, 0x12, 0x71, 0x0a, 0x10, 0x46, 0x69, 0x6e, 0x64, 0x50,
	0x65, 0x74, 0x73, 0x42, 0x79, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x12, 0x21, 0x2e, 0x69, 0x6f,
	0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72,
	0x65, 0x2e, 0x76, 0x32, 0x2e, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x52, 0x65, 0x71, 0x1a, 0x1c,
	0x2e, 0x69, 0x6f, 0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65, 0x74, 0x73,
	0x74, 0x6f, 0x72, 0x65, 0x2e, 0x76, 0x32, 0x2e, 0x50, 0x65, 0x74, 0x73, 0x22, 0x1c, 0x82, 0xd3,
	0xe4, 0x93, 0x02, 0x13, 0x12, 0x11, 0x2f, 0x70, 0x65, 0x74, 0x2f, 0x66, 0x69, 0x6e, 0x64, 0x42,
	0x79, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x90, 0x02, 0x01, 0x42, 0x80, 0x02, 0x0a, 0x1a, 0x63,
	0x6f, 0x6d, 0x2e, 0x69, 0x6f, 0x2e, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x70, 0x65,
	0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2e, 0x76, 0x32, 0x42, 0x09, 0x50, 0x65, 0x74, 0x73, 0x50,
	0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x5c, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63,
	0x6f, 0x6d, 0x2f, 0x62, 0x75, 0x66, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x2f, 0x76, 0x61, 0x6e, 0x67,
	0x75, 0x61, 0x72, 0x64, 0x2d, 0x67, 0x6f, 0x2f, 0x65, 0x78, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x73,
	0x2f, 0x70, 0x65, 0x74, 0x73, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x67,
	0x65, 0x6e, 0x2f, 0x69, 0x6f, 0x2f, 0x73, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2f, 0x70, 0x65,
	0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x2f, 0x76, 0x32, 0x3b, 0x70, 0x65, 0x74, 0x73, 0x74, 0x6f,
	0x72, 0x65, 0x76, 0x32, 0xa2, 0x02, 0x03, 0x49, 0x53, 0x50, 0xaa, 0x02, 0x16, 0x49, 0x6f, 0x2e,
	0x53, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x50, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65,
	0x2e, 0x56, 0x32, 0xca, 0x02, 0x16, 0x49, 0x6f, 0x5c, 0x53, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72,
	0x5c, 0x50, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x5c, 0x56, 0x32, 0xe2, 0x02, 0x22, 0x49,
	0x6f, 0x5c, 0x53, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x5c, 0x50, 0x65, 0x74, 0x73, 0x74, 0x6f,
	0x72, 0x65, 0x5c, 0x56, 0x32, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74,
	0x61, 0xea, 0x02, 0x19, 0x49, 0x6f, 0x3a, 0x3a, 0x53, 0x77, 0x61, 0x67, 0x67, 0x65, 0x72, 0x3a,
	0x3a, 0x50, 0x65, 0x74, 0x73, 0x74, 0x6f, 0x72, 0x65, 0x3a, 0x3a, 0x56, 0x32, 0x62, 0x06, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_io_swagger_petstore_v2_pets_proto_rawDescOnce sync.Once
	file_io_swagger_petstore_v2_pets_proto_rawDescData = file_io_swagger_petstore_v2_pets_proto_rawDesc
)

func file_io_swagger_petstore_v2_pets_proto_rawDescGZIP() []byte {
	file_io_swagger_petstore_v2_pets_proto_rawDescOnce.Do(func() {
		file_io_swagger_petstore_v2_pets_proto_rawDescData = protoimpl.X.CompressGZIP(file_io_swagger_petstore_v2_pets_proto_rawDescData)
	})
	return file_io_swagger_petstore_v2_pets_proto_rawDescData
}

var file_io_swagger_petstore_v2_pets_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_io_swagger_petstore_v2_pets_proto_msgTypes = make([]protoimpl.MessageInfo, 11)
var file_io_swagger_petstore_v2_pets_proto_goTypes = []interface{}{
	(Status)(0),                  // 0: io.swagger.petstore.v2.Status
	(*Category)(nil),             // 1: io.swagger.petstore.v2.Category
	(*Tag)(nil),                  // 2: io.swagger.petstore.v2.Tag
	(*PetID)(nil),                // 3: io.swagger.petstore.v2.PetID
	(*Pet)(nil),                  // 4: io.swagger.petstore.v2.Pet
	(*UpdatePetWithFormReq)(nil), // 5: io.swagger.petstore.v2.UpdatePetWithFormReq
	(*UploadFileReq)(nil),        // 6: io.swagger.petstore.v2.UploadFileReq
	(*PetBody)(nil),              // 7: io.swagger.petstore.v2.PetBody
	(*StatusReq)(nil),            // 8: io.swagger.petstore.v2.StatusReq
	(*TagReq)(nil),               // 9: io.swagger.petstore.v2.TagReq
	(*Pets)(nil),                 // 10: io.swagger.petstore.v2.Pets
	(*ApiResponse)(nil),          // 11: io.swagger.petstore.v2.ApiResponse
	(*emptypb.Empty)(nil),        // 12: google.protobuf.Empty
}
var file_io_swagger_petstore_v2_pets_proto_depIdxs = []int32{
	1,  // 0: io.swagger.petstore.v2.Pet.category:type_name -> io.swagger.petstore.v2.Category
	2,  // 1: io.swagger.petstore.v2.Pet.tags:type_name -> io.swagger.petstore.v2.Tag
	4,  // 2: io.swagger.petstore.v2.Pets.pets:type_name -> io.swagger.petstore.v2.Pet
	3,  // 3: io.swagger.petstore.v2.PetService.GetPetByID:input_type -> io.swagger.petstore.v2.PetID
	5,  // 4: io.swagger.petstore.v2.PetService.UpdatePetWithForm:input_type -> io.swagger.petstore.v2.UpdatePetWithFormReq
	3,  // 5: io.swagger.petstore.v2.PetService.DeletePet:input_type -> io.swagger.petstore.v2.PetID
	6,  // 6: io.swagger.petstore.v2.PetService.UploadFile:input_type -> io.swagger.petstore.v2.UploadFileReq
	4,  // 7: io.swagger.petstore.v2.PetService.AddPet:input_type -> io.swagger.petstore.v2.Pet
	4,  // 8: io.swagger.petstore.v2.PetService.UpdatePet:input_type -> io.swagger.petstore.v2.Pet
	9,  // 9: io.swagger.petstore.v2.PetService.FindPetsByTag:input_type -> io.swagger.petstore.v2.TagReq
	8,  // 10: io.swagger.petstore.v2.PetService.FindPetsByStatus:input_type -> io.swagger.petstore.v2.StatusReq
	4,  // 11: io.swagger.petstore.v2.PetService.GetPetByID:output_type -> io.swagger.petstore.v2.Pet
	12, // 12: io.swagger.petstore.v2.PetService.UpdatePetWithForm:output_type -> google.protobuf.Empty
	12, // 13: io.swagger.petstore.v2.PetService.DeletePet:output_type -> google.protobuf.Empty
	11, // 14: io.swagger.petstore.v2.PetService.UploadFile:output_type -> io.swagger.petstore.v2.ApiResponse
	4,  // 15: io.swagger.petstore.v2.PetService.AddPet:output_type -> io.swagger.petstore.v2.Pet
	4,  // 16: io.swagger.petstore.v2.PetService.UpdatePet:output_type -> io.swagger.petstore.v2.Pet
	10, // 17: io.swagger.petstore.v2.PetService.FindPetsByTag:output_type -> io.swagger.petstore.v2.Pets
	10, // 18: io.swagger.petstore.v2.PetService.FindPetsByStatus:output_type -> io.swagger.petstore.v2.Pets
	11, // [11:19] is the sub-list for method output_type
	3,  // [3:11] is the sub-list for method input_type
	3,  // [3:3] is the sub-list for extension type_name
	3,  // [3:3] is the sub-list for extension extendee
	0,  // [0:3] is the sub-list for field type_name
}

func init() { file_io_swagger_petstore_v2_pets_proto_init() }
func file_io_swagger_petstore_v2_pets_proto_init() {
	if File_io_swagger_petstore_v2_pets_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_io_swagger_petstore_v2_pets_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Category); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_io_swagger_petstore_v2_pets_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Tag); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_io_swagger_petstore_v2_pets_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PetID); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_io_swagger_petstore_v2_pets_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Pet); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_io_swagger_petstore_v2_pets_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*UpdatePetWithFormReq); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_io_swagger_petstore_v2_pets_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*UploadFileReq); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_io_swagger_petstore_v2_pets_proto_msgTypes[6].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PetBody); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_io_swagger_petstore_v2_pets_proto_msgTypes[7].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*StatusReq); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_io_swagger_petstore_v2_pets_proto_msgTypes[8].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TagReq); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_io_swagger_petstore_v2_pets_proto_msgTypes[9].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Pets); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_io_swagger_petstore_v2_pets_proto_msgTypes[10].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ApiResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_io_swagger_petstore_v2_pets_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   11,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_io_swagger_petstore_v2_pets_proto_goTypes,
		DependencyIndexes: file_io_swagger_petstore_v2_pets_proto_depIdxs,
		EnumInfos:         file_io_swagger_petstore_v2_pets_proto_enumTypes,
		MessageInfos:      file_io_swagger_petstore_v2_pets_proto_msgTypes,
	}.Build()
	File_io_swagger_petstore_v2_pets_proto = out.File
	file_io_swagger_petstore_v2_pets_proto_rawDesc = nil
	file_io_swagger_petstore_v2_pets_proto_goTypes = nil
	file_io_swagger_petstore_v2_pets_proto_depIdxs = nil
}
