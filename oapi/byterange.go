package oapi

// ByteRange is a part of a multipart/byteranges response (RFC 9110,
// section 14.6): the bytes Start to End, inclusive, of a representation
// of Size bytes, or of an unknown size if Size is -1, whose media type is
// ContentType.
type ByteRange struct {
	Start, End, Size int64
	ContentType      string
	Data             []byte
}
