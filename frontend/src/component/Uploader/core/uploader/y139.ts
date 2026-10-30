import { s3LikeUploadCallback, y139UploadChunk } from "../api";
import { PolicyType } from "../../../../api/explorer.ts";
import { Status } from "./base";
import Chunk, { ChunkInfo } from "./chunk";

/**
 * Y139 uploads one part of a file straight to the China Mobile cloud drive (139)
 * object storage. The backend only signs the part URLs and finalizes the upload,
 * so no file content passes through Cloudreve.
 */
export default class Y139 extends Chunk {
  // The drive answers /file/create with `rapidUpload` / `exist` when it already
  // holds this content: the backend then signs no part URL at all, and the
  // upload runs with nothing to transfer. afterUpload() below still has to be
  // called in that case — it is what completes the upload on our side.
  protected async uploadChunk(chunkInfo: ChunkInfo) {
    const session = this.task.session!;
    const url = session.upload_urls[chunkInfo.index];
    if (!url) {
      throw new Error(`No upload URL signed for part ${chunkInfo.index + 1}`);
    }

    await y139UploadChunk(
      url,
      chunkInfo,
      (p) => {
        this.updateChunkProgress(p.loaded, chunkInfo.index);
      },
      this.cancelToken.token,
      chunkInfo.index + 1,
      this.task.file.name,
    );
  }

  protected async afterUpload(): Promise<any> {
    this.logger.info("Notifying the backend to finalize the 139 cloud drive upload...");
    this.transit(Status.finishing);

    // There is no client side complete call: the backend runs /file/complete
    // itself (CompleteUpload) when it receives this callback.
    return s3LikeUploadCallback(
      this.task.session!.session_id,
      this.task.session!.callback_secret,
      PolicyType.y139,
    );
  }
}
