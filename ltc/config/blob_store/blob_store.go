package blob_store

import (
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/cloudfoundry-incubator/lattice/ltc/config"
)

type BlobStore struct {
	Bucket string
	S3     *s3.S3
}

type Blob struct {
	Path    string
	Created time.Time
	Size    int64
}

func New(blobTarget config.BlobTargetInfo) *BlobStore {
	endpoint := fmt.Sprintf("http://%s:%d/", blobTarget.TargetHost, blobTarget.TargetPort)
	client := s3.New(&aws.Config{
		Credentials:      credentials.NewStaticCredentials(blobTarget.AccessKey, blobTarget.SecretKey, ""),
		Endpoint:         endpoint,
		Region:           "riak-region-1",
		S3ForcePathStyle: true,
	})

	client.Handlers.Sign.Clear()
	client.Handlers.Sign.PushBack(aws.BuildContentLength)
	client.Handlers.Sign.PushBack(func(request *aws.Request) {
		v2Sign(blobTarget.AccessKey, blobTarget.SecretKey, request.Time, request.HTTPRequest)
	})

	return &BlobStore{
		Bucket: blobTarget.BucketName,
		S3:     client,
	}
}

func (b *BlobStore) List() ([]Blob, error) {
	objects, err := b.S3.ListObjects(&s3.ListObjectsInput{
		Bucket: aws.String(b.Bucket),
	})
	if err != nil {
		return nil, err
	}

	blobs := []Blob{}
	for _, obj := range objects.Contents {
		blobs = append(blobs, Blob{
			Path:    *obj.Key,
			Size:    *obj.Size,
			Created: *obj.LastModified,
		})
	}

	return blobs, nil
}

func (b *BlobStore) Upload(path string, contents io.ReadSeeker) error {
	_, err := b.S3.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(b.Bucket),
		ACL:    aws.String("private"),
		Key:    aws.String(path),
		Body:   contents,
	})
	return err
}

func (b *BlobStore) Download(path string) (io.ReadCloser, error) {
	output, err := b.S3.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(b.Bucket),
		Key:    aws.String(path),
	})
	return output.Body, err
}

func (b *BlobStore) Delete(path string) error {
	_, err := b.S3.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(b.Bucket),
		Key:    aws.String(path),
	})
	return err
}
