package openlist

import (
	"context"
	"fmt"
	"io"
	"path"

	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
)

// ensureFolder creates dir and every missing parent on OpenList.
//
// Cloudreve only tracks folders in its own index, so a folder the user created
// here has no counterpart on the storage until something is written into it.
// Writing therefore creates the parents on demand instead of mirroring every
// mkdir call: an empty folder on this site is a legitimate state that costs
// nothing on the storage.
func (handler *Driver) ensureFolder(ctx context.Context, dir string) error {
	dir = normalizePath(dir)
	if dir == "" {
		return nil
	}

	return handler.client.Mkdir(ctx, dir)
}

// putStream writes a whole object in one request.
//
// This is the path for a single-shot write: WebDAV PUT, an overwrite of an
// existing entity, and any file small enough that the relay route does not
// bother splitting it.
func (handler *Driver) putStream(ctx context.Context, file *fs.UploadRequest) error {
	target := normalizePath(file.Props.SavePath)
	if target == "" {
		return fs.ErrIllegalObjectName.WithError(fmt.Errorf("empty save path"))
	}

	if err := handler.ensureFolder(ctx, parentOf(target)); err != nil {
		return err
	}

	size := file.Props.Size
	if size <= 0 {
		// An unknown size means the caller streams until EOF; OpenList accepts
		// a chunked body for that, and it reads the body to EOF to size it.
		size = -1
	}

	if file.Seeker != nil {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("failed to rewind the upload stream: %w", err)
		}
	}

	return handler.client.PutStream(ctx, target, file, size)
}

// uploadMultipart writes the whole content through OpenList's multipart API,
// resuming a session OpenList still holds for the same path and size.
func (handler *Driver) uploadMultipart(ctx context.Context, file *fs.UploadRequest) error {
	target := normalizePath(file.Props.SavePath)
	if target == "" {
		return fs.ErrIllegalObjectName.WithError(fmt.Errorf("empty save path"))
	}

	size := file.Props.Size
	if size <= 0 {
		// Without a declared size multipart cannot split the content, and the
		// session cannot be found again by path+size: fall back to one write.
		return handler.putStream(ctx, file)
	}

	if err := handler.ensureFolder(ctx, parentOf(target)); err != nil {
		return err
	}

	session, err := handler.client.MultipartInit(ctx, target, size, handler.chunkSize)
	if err != nil {
		return err
	}

	chunkSize := session.ChunkSize
	if chunkSize <= 0 {
		chunkSize = size
	}

	total := int((size + chunkSize - 1) / chunkSize)
	if session.Resumed && session.ReceivedByte > 0 {
		handler.l.Debug("Resuming the OpenList upload of %q at %d/%d bytes.", target, session.ReceivedByte, size)
	}

	reader := io.Reader(file)
	for index := 0; index < total; index++ {
		offset := int64(index) * chunkSize
		length := chunkSize
		if offset+length > size {
			length = size - offset
		}

		if session.HasChunk(index) {
			// Already on OpenList from the interrupted attempt: consume the
			// matching part of the stream so later chunks line up, without
			// sending it again.
			if _, err := io.CopyN(io.Discard, reader, length); err != nil {
				return fmt.Errorf("failed to skip chunk %d of %q: %w", index+1, target, err)
			}
			continue
		}

		if err := handler.client.MultipartChunk(ctx, session.UploadID, index,
			io.LimitReader(reader, length), length); err != nil {
			return fmt.Errorf("failed to upload chunk %d of %q: %w", index+1, target, err)
		}
	}

	// OpenList assembles and promotes the object as soon as the last missing
	// chunk lands, and drops the session at that point. Asking it to complete
	// again then answers "session not found", so check first and only call
	// complete for the rare case where the object is still pending.
	if after, err := handler.client.MultipartStatus(ctx, target, size); err == nil &&
		after.State == multipartStateCompleted {
		return nil
	}

	return handler.client.MultipartComplete(ctx, session.UploadID)
}

// renameFileOnStorage renames one object in place, so the storage keeps
// matching the file tree this site shows.
func (handler *Driver) renameFileOnStorage(ctx context.Context, from, newName string) error {
	from = normalizePath(from)
	if err := validateUserPath(from); err != nil {
		return err
	}

	if err := handler.client.Rename(ctx, from, newName); err != nil {
		// A name the storage refuses is the user's mistake, not a storage
		// problem: report it as an illegal object name.
		if isAlreadyExists(err) {
			return fs.ErrFileExisted.WithError(err)
		}
		return err
	}

	return nil
}

// moveFileOnStorage moves one object into the folder dstDir.
func (handler *Driver) moveFileOnStorage(ctx context.Context, from, dstDir string) error {
	from = normalizePath(from)
	dstDir = normalizePath(dstDir)
	if err := validateUserPath(from); err != nil {
		return err
	}

	parent := parentOf(from)
	if parent == dstDir {
		// Already there, nothing to do on the storage side.
		return nil
	}

	if err := handler.ensureFolder(ctx, dstDir); err != nil {
		return err
	}

	return handler.client.Move(ctx, []string{from}, dstDir)
}

// objectName returns the file name a path ends with.
func objectName(p string) string {
	return path.Base(normalizePath(p))
}
