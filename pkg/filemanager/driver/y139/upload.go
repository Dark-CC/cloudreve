package y139

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/chunk"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/chunk/backoff"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
)

const (
	// hashAlgorithmSHA256 is the only algorithm the drive accepts today.
	hashAlgorithmSHA256 = "SHA256"
	// renameModeAuto keeps both objects and gives the new one a free name.
	renameModeAuto = "auto_rename"
)

// partInfo describes one part in the create request. The drive hashes each part
// at its own offset, which is what parallelHashCtx carries.
type partInfo struct {
	PartNumber      int64           `json:"partNumber"`
	PartSize        int64           `json:"partSize"`
	ParallelHashCtx parallelHashCtx `json:"parallelHashCtx"`
}

type parallelHashCtx struct {
	PartOffset int64 `json:"partOffset"`
}

// uploadedPart is a pre-signed part URL returned by the drive.
type uploadedPart struct {
	PartNumber int    `json:"partNumber"`
	UploadUrl  string `json:"uploadUrl"`
}

// commonAccountInfo identifies the account in the upload calls.
type commonAccountInfo struct {
	Account     string `json:"account"`
	AccountType int    `json:"accountType"`
}

// createFileRequest is the payload of /file/create for a regular file.
type createFileRequest struct {
	ContentHash          string            `json:"contentHash"`
	ContentHashAlgorithm string            `json:"contentHashAlgorithm"`
	ContentType          string            `json:"contentType"`
	ParallelUpload       bool              `json:"parallelUpload"`
	PartInfos            []partInfo        `json:"partInfos"`
	Size                 int64             `json:"size"`
	ParentFileID         string            `json:"parentFileId"`
	Name                 string            `json:"name"`
	Type                 string            `json:"type"`
	FileRenameMode       string            `json:"fileRenameMode"`
	CommonAccountInfo    commonAccountInfo `json:"commonAccountInfo"`
}

// createFileResponse is the data of a create call.
type createFileResponse struct {
	FileID      string         `json:"fileId"`
	FileName    string         `json:"fileName"`
	PartInfos   []uploadedPart `json:"partInfos"`
	Exist       bool           `json:"exist"`
	RapidUpload bool           `json:"rapidUpload"`
	UploadID    string         `json:"uploadId"`
}

// getUploadURLRequest asks for the part URLs beyond the first batch.
type getUploadURLRequest struct {
	FileID            string            `json:"fileId"`
	UploadID          string            `json:"uploadId"`
	PartInfos         []partInfo        `json:"partInfos"`
	CommonAccountInfo commonAccountInfo `json:"commonAccountInfo"`
}

type getUploadURLResponse struct {
	FileID    string         `json:"fileId"`
	UploadID  string         `json:"uploadId"`
	PartInfos []uploadedPart `json:"partInfos"`
}

// completeRequest finalizes a multipart upload.
type completeRequest struct {
	ContentHash          string `json:"contentHash"`
	ContentHashAlgorithm string `json:"contentHashAlgorithm"`
	FileID               string `json:"fileId"`
	UploadID             string `json:"uploadId"`
}

// CreateRequest is the driver level request for a multipart upload.
type CreateRequest struct {
	ParentFileID string
	Name         string
	Size         int64
	ChunkSize    int64
	ContentHash  string
	ContentType  string
	Account      string
}

// CreateResponse carries the signed part URLs of the first batch.
type CreateResponse struct {
	FileID      string
	FileName    string
	UploadID    string
	Exist       bool
	RapidUpload bool
	Parts       []uploadedPart
}

// CompleteRequest finalizes a multipart upload.
type CompleteRequest struct {
	FileID      string
	UploadID    string
	ContentHash string
}

// numParts returns how many parts a file of total bytes needs.
func numParts(total, chunkSize int64) int {
	if total <= 0 || chunkSize <= 0 {
		return 1
	}
	n := int(total / chunkSize)
	if total%chunkSize != 0 {
		n++
	}
	return n
}

// partSize returns the size of the part with the given 0-based index, the last
// part holding the remainder.
func partSize(total, chunkSize int64, index, num int) int64 {
	if num <= 0 {
		return 0
	}
	if index == num-1 {
		return total - chunkSize*int64(num-1)
	}
	return chunkSize
}

// buildPartInfos builds the part table sent to the drive.
func buildPartInfos(total, chunkSize int64) []partInfo {
	num := numParts(total, chunkSize)
	infos := make([]partInfo, 0, num)
	for i := 0; i < num; i++ {
		infos = append(infos, partInfo{
			// The drive numbers parts from 1.
			PartNumber: int64(i + 1),
			PartSize:   partSize(total, chunkSize, i, num),
			ParallelHashCtx: parallelHashCtx{
				PartOffset: int64(i) * chunkSize,
			},
		})
	}
	return infos
}

// Create asks the drive to create the placeholder and to sign the first batch of
// part upload URLs (at most 100 parts per call).
func (c *client) Create(ctx context.Context, req *CreateRequest) (*CreateResponse, error) {
	infos := buildPartInfos(req.Size, req.ChunkSize)
	firstBatch := infos
	if len(firstBatch) > partBatchSize {
		firstBatch = firstBatch[:partBatchSize]
	}

	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	payload := &createFileRequest{
		ContentHash:          req.ContentHash,
		ContentHashAlgorithm: hashAlgorithmSHA256,
		ContentType:          contentType,
		ParallelUpload:       false,
		PartInfos:            firstBatch,
		Size:                 req.Size,
		ParentFileID:         req.ParentFileID,
		Name:                 req.Name,
		Type:                 fileTypeFile,
		FileRenameMode:       renameModeAuto,
		CommonAccountInfo:    commonAccountInfo{Account: req.Account, AccountType: 1},
	}

	var res createFileResponse
	if err := c.call(ctx, apiCreate, payload, &res); err != nil {
		return nil, err
	}

	return &CreateResponse{
		FileID:      res.FileID,
		FileName:    res.FileName,
		UploadID:    res.UploadID,
		Exist:       res.Exist,
		RapidUpload: res.RapidUpload,
		Parts:       res.PartInfos,
	}, nil
}

// GetUploadURL signs the part URLs beyond the first batch.
func (c *client) GetUploadURL(ctx context.Context, fileID, uploadID string, infos []partInfo) ([]uploadedPart, error) {
	var res getUploadURLResponse
	if err := c.call(ctx, apiGetUploadURL, &getUploadURLRequest{
		FileID:            fileID,
		UploadID:          uploadID,
		PartInfos:         infos,
		CommonAccountInfo: commonAccountInfo{Account: c.accountName(), AccountType: 1},
	}, &res); err != nil {
		return nil, err
	}

	return res.PartInfos, nil
}

// Complete finalizes a multipart upload so the drive materializes the file.
func (c *client) Complete(ctx context.Context, req *CompleteRequest) error {
	return c.call(ctx, apiComplete, &completeRequest{
		ContentHash:          req.ContentHash,
		ContentHashAlgorithm: hashAlgorithmSHA256,
		FileID:               req.FileID,
		UploadID:             req.UploadID,
	}, nil)
}

// UploadParts pushes every part of file to its pre-signed URL. The signature
// covers content-length, so it is set explicitly, and no session credential is
// forwarded: the target is the object storage, not the drive.
func (c *client) UploadParts(ctx context.Context, file *fs.UploadRequest, parts []uploadedPart, chunkSize int64) error {
	byNumber := make(map[int]string, len(parts))
	for _, part := range parts {
		byNumber[part.PartNumber] = part.UploadUrl
	}

	expected := numParts(file.Props.Size, chunkSize)
	if len(byNumber) != expected {
		return fmt.Errorf("cloud drive signed %d parts but %d were expected", len(byNumber), expected)
	}

	u := &partUploader{client: c, chunkSize: chunkSize, parts: byNumber}
	return u.upload(ctx, file)
}

// putPart uploads one part from reader to target.
func (c *client) putPart(ctx context.Context, target string, reader io.Reader, length int64) error {
	res := c.http.Request(http.MethodPut, target, reader,
		request.WithContext(ctx),
		request.WithContentLength(length),
		request.WithHeader(http.Header{
			"Content-Type": []string{"application/octet-stream"},
			"Origin":       []string{"https://yun.139.com"},
			"Referer":      []string{"https://yun.139.com/"},
		}),
	)
	if res.Err != nil {
		return &backoff.RetryableError{Err: res.Err}
	}

	_, err := res.GetResponse()
	if err != nil {
		return &backoff.RetryableError{Err: err}
	}

	if res.Response.StatusCode < 200 || res.Response.StatusCode >= 300 {
		return &backoff.RetryableError{
			Err: fmt.Errorf("object storage returned HTTP %d for a part upload", res.Response.StatusCode),
		}
	}

	return nil
}

// partUploader streams the parts of a seekable upload to their signed URLs.
type partUploader struct {
	client    *client
	chunkSize int64
	parts     map[int]string
}

// upload streams the whole part set.
func (u *partUploader) upload(ctx context.Context, file *fs.UploadRequest) error {
	chunks := chunk.NewChunkGroup(file, u.chunkSize, &backoff.ConstantBackoff{Sleep: time.Second, Max: 3}, false, u.client.l, "")
	for chunks.Next() {
		index := chunks.Index()
		target, ok := u.parts[index+1]
		if !ok || target == "" {
			return fmt.Errorf("cloud drive did not sign part %d", index+1)
		}

		err := chunks.Process(func(cg *chunk.ChunkGroup, reader io.Reader) error {
			return u.client.putPart(ctx, target, reader, cg.Length())
		})
		if err != nil {
			return err
		}
	}

	return nil
}
