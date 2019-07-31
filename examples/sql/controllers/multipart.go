package controllers

import (
	"io"
	"io/ioutil"
	"net/http"

	"github.com/jinzhu/gorm"
	"github.com/pachyderm/s2"
	"github.com/pachyderm/s2/examples/sql/models"
	"github.com/pachyderm/s2/examples/sql/util"
)

func (c Controller) ListMultipart(r *http.Request, name, keyMarker, uploadIDMarker string, maxUploads int) (isTruncated bool, uploads []s2.Upload, err error) {
	c.logger.Tracef("ListMultipart: name=%+v, keyMarker=%+v, uploadIDMarker=%+v, maxUploads=%+v", name, keyMarker, uploadIDMarker, maxUploads)
	tx := c.db.Begin()

	var bucket models.Bucket
	bucket, err = models.GetBucket(tx, name)
	if err != nil {
		c.rollback(tx)
		if gorm.IsRecordNotFoundError(err) {
			err = s2.NoSuchBucketError(r)
		}
		return
	}

	parts, err := models.ListMultiparts(tx, bucket.ID, keyMarker, uploadIDMarker, maxUploads+1)
	if err != nil {
		c.rollback(tx)
		return
	}

	for _, part := range parts {
		if len(uploads) >= maxUploads {
			if maxUploads > 0 {
				isTruncated = true
			}
			break
		}

		uploads = append(uploads, s2.Upload{
			Key:          part.Key,
			UploadID:     part.UploadID,
			Initiator:    models.GlobalUser,
			StorageClass: models.StorageClass,
			Initiated:    models.Epoch,
		})
	}

	c.commit(tx)
	return
}

func (c Controller) InitMultipart(r *http.Request, name, key string) (uploadID string, err error) {
	c.logger.Tracef("InitMultipart: name=%+v, key=%+v", name, key)
	tx := c.db.Begin()

	_, err = models.GetBucket(tx, name)
	if err != nil {
		c.rollback(tx)
		if gorm.IsRecordNotFoundError(err) {
			err = s2.NoSuchBucketError(r)
		}
		return
	}

	c.commit(tx)
	uploadID = util.RandomString(10)
	return
}

func (c Controller) AbortMultipart(r *http.Request, name, key, uploadID string) error {
	c.logger.Tracef("AbortMultipart: name=%+v, key=%+v, uploadID=%+v", name, key, uploadID)
	tx := c.db.Begin()

	bucket, err := models.GetBucket(tx, name)
	if err != nil {
		c.rollback(tx)
		if gorm.IsRecordNotFoundError(err) {
			return s2.NoSuchBucketError(r)
		}
		return err
	}

	err = models.DeleteMultiparts(tx, bucket.ID, key, uploadID)
	if err != nil {
		c.rollback(tx)
		return err
	}

	c.commit(tx)
	return nil
}

func (c Controller) CompleteMultipart(r *http.Request, name, key, uploadID string, parts []s2.Part) (location, etag string, err error) {
	c.logger.Tracef("CompleteMultipart: name=%+v, key=%+v, uploadID=%+v, parts=%+v", name, key, uploadID, parts)
	tx := c.db.Begin()

	var bucket models.Bucket
	bucket, err = models.GetBucket(tx, name)
	if err != nil {
		c.rollback(tx)
		if gorm.IsRecordNotFoundError(err) {
			err = s2.NoSuchBucketError(r)
		}
		return
	}

	bytes := []byte{}

	for i, part := range parts {
		var chunk models.Multipart
		chunk, err = models.GetMultipart(tx, bucket.ID, key, uploadID, part.PartNumber)
		if err != nil {
			c.rollback(tx)
			if gorm.IsRecordNotFoundError(err) {
				err = s2.InvalidPartError(r)
			}
			return
		}
		if chunk.ETag != part.ETag {
			c.rollback(tx)
			err = s2.InvalidPartError(r)
			return
		}
		if i < len(parts)-1 && len(chunk.Content) < 5*1024*1024 {
			c.rollback(tx)
			// each part, except for the last, is expected to be at least 5mb
			// in s3
			err = s2.EntityTooSmallError(r)
			return
		}

		bytes = append(bytes, chunk.Content...)
	}

	var obj models.Object
	obj, err = models.UpsertObject(tx, bucket.ID, key, bytes)
	if err != nil {
		c.rollback(tx)
		return
	}
	if err = models.DeleteMultiparts(tx, bucket.ID, key, uploadID); err != nil {
		c.rollback(tx)
		return
	}

	location = models.Location
	etag = obj.ETag
	c.commit(tx)
	return
}

func (c Controller) ListMultipartChunks(r *http.Request, name, key, uploadID string, partNumberMarker, maxParts int) (initiator, owner *s2.User, storageClass string, isTruncated bool, parts []s2.Part, err error) {
	c.logger.Tracef("ListMultipartChunks: name=%+v, key=%+v, uploadID=%+v, partNumberMarker=%+v, maxParts=%+v", name, key, uploadID, partNumberMarker, maxParts)
	tx := c.db.Begin()

	var bucket models.Bucket
	bucket, err = models.GetBucket(tx, name)
	if err != nil {
		c.rollback(tx)
		if gorm.IsRecordNotFoundError(err) {
			err = s2.NoSuchBucketError(r)
		}
		return
	}

	var chunks []models.Multipart
	chunks, err = models.ListMultipartChunks(tx, bucket.ID, key, uploadID, partNumberMarker, maxParts+1)
	if err != nil {
		c.rollback(tx)
		return
	}

	for _, chunk := range chunks {
		if len(parts) >= maxParts {
			if maxParts > 0 {
				isTruncated = true
			}
			break
		}

		parts = append(parts, s2.Part{
			PartNumber: chunk.PartNumber,
			ETag:       chunk.ETag,
		})
	}

	initiator = &models.GlobalUser
	owner = &models.GlobalUser
	storageClass = models.StorageClass
	c.commit(tx)
	return
}

func (c Controller) UploadMultipartChunk(r *http.Request, name, key, uploadID string, partNumber int, reader io.Reader) (etag string, err error) {
	c.logger.Tracef("UploadMultipartChunk: name=%+v, key=%+v, uploadID=%+v partNumber=%+v", name, key, uploadID, partNumber)
	tx := c.db.Begin()

	var bucket models.Bucket
	bucket, err = models.GetBucket(tx, name)
	if err != nil {
		c.rollback(tx)
		if gorm.IsRecordNotFoundError(err) {
			err = s2.NoSuchBucketError(r)
		}
		return
	}

	bytes, err := ioutil.ReadAll(reader)
	if err != nil {
		c.rollback(tx)
		return
	}

	multipart, err := models.UpsertMultipart(tx, bucket.ID, key, uploadID, partNumber, bytes)
	if err != nil {
		c.rollback(tx)
		return
	}

	etag = multipart.ETag
	c.commit(tx)
	return
}
