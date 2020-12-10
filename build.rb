require 'dotenv'
Dotenv.load
require 'aws-sdk-dynamodb'
require 'aws-sdk-s3'
require 'erb'
require 'open-uri'

DEBUG = ARGV.join =~ /test/

# -------------------
# BUILD THE OUTPUT JSON AND HTML
# -------------------

opml = URI.open("http://#{ENV['S3_BUCKET_NAME']}.s3.#{ENV['AWS_DEFAULT_REGION']}.amazonaws.com/#{ENV['S3_BUCKET_NAME']}.opml").read

# Can't be bothered with the dependencies, so let's go oldschool..
feeds = opml.scan(/<outline.*\/>/)

source_count = feeds.size

dynamodb = Aws::DynamoDB::Client.new
s3 = Aws::S3::Client.new(region: ENV['AWS_DEFAULT_REGION'])

# We can scan because we're using DynamoDB's TTL feature to automatically
# cull old entries.
result = dynamodb.scan(table_name: ENV['DYNAMODB_TABLE_NAME'])
items = result.items.sort_by { |r| r['published'] }.reverse

# Write out a JSON with all the items and store on S3
s3.put_object(bucket: ENV['S3_BUCKET_NAME'], key: 'entries.json', body: items.to_json, content_type: 'application/json', cache_control: "max-age=600")
s3.put_object_acl({ acl: "public-read", bucket: ENV['S3_BUCKET_NAME'], key: 'entries.json' })

# Map items into a slightly more useful structure for ERB
items = items.map do |item|
  {
    published: Time.parse(item['published']),
    title: item['title'],
    url: item['url'],
    feed: item['feed'],
    feed_site: item['feed_site']
  }
end

# Don't show future items
items.delete_if { |item| item[:published] > Time.now }

# Write out an HTML file with all the items rendered through our template
res = ERB.new(File.read("template.erb")).result(binding)

if DEBUG
  STDERR.puts "Uploading to test.html"
  s3.put_object(bucket: ENV['S3_BUCKET_NAME'], key: 'test.html', body: res, content_type: 'text/html;charset=utf-8', cache_control: "max-age=0")
  s3.put_object_acl({ acl: "public-read", bucket: ENV['S3_BUCKET_NAME'], key: 'test.html' })
else
  STDERR.puts "Uploading to index.html"
  s3.put_object(bucket: ENV['S3_BUCKET_NAME'], key: 'index.html', body: res, content_type: 'text/html;charset=utf-8', cache_control: "max-age=600")
  s3.put_object_acl({ acl: "public-read", bucket: ENV['S3_BUCKET_NAME'], key: 'index.html' })
end

STDERR.puts "Uploaded"
