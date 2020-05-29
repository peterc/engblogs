# Cleans up the OPML file and gets rid of any feeds that no
# longer load or parse properly.
#
# ruby redo-opml.rb > new.opml
# aws s3 cp new.opml s3://engblogs/engblogs.opml

require 'dotenv'
Dotenv.load
require 'open-uri'
require 'feedjira'
require 'thread/pool'

semaphore = Mutex.new

pool = Thread.pool(40)

opml = URI.open("http://#{ENV['S3_BUCKET_NAME']}.#{ENV['AWS_DEFAULT_REGION']}.amazonaws.com/#{ENV['S3_BUCKET_NAME']}.opml").read

feeds = opml.scan(/<outline.*\/>/)

feeds.map! do |feed| 
  {
    title: feed[/title=(\"|\')(.*?)\1/i, 2],
    xmlurl: feed[/xmlurl=(\"|\')(.*?)\1/i, 2],
    htmlurl: feed[/htmlurl=(\"|\')(.*?)\1/i, 2],
  }
end

puts %{<?xml version="1.0" encoding="UTF-8"?>
  <opml version="1.0">
    <head>
      <title>Engineering Blogs</title>
    </head>
    <body>
      <outline text="Engineering Blogs" title="Engineering Blogs">}

feeds.each do |feed|
  pool.process do

    STDERR.puts "Doing #{feed[:title]}"
    begin
      xml = URI.open(feed[:xmlurl], "User-Agent" => "My RSS Reader").read
    rescue Net::OpenTimeout, OpenSSL::SSL::SSLError, SocketError, OpenURI::HTTPError, URI::InvalidURIError => e
      STDERR.puts "  FAILURE #{e}"
      next
    end

    xml.sub!(/\<\?.*?\?\>/, '')
    begin
      pfeed = Feedjira.parse(xml)
    rescue Feedjira::NoParserAvailable
      STDERR.puts "  FAILURE"
      next
    end
    entries = pfeed.entries.map do |entry|
      {
        published: entry.published,
        title: entry.title,
        url: entry.url
      }
    end

    entries = entries.select { |entry| (Time.now - entry[:published]) < (86400 * 7) }
    STDERR.puts "  Fetched #{pfeed.entries.size} entries, #{entries.size} recent"

    opml_string = %{<outline type="rss" text="#{feed[:title]}" title="#{feed[:title]}" xmlUrl="#{feed[:xmlurl]}" htmlUrl="#{feed[:htmlurl]}"/>}

    semaphore.synchronize do
      puts opml_string
    end
  end
end

pool.shutdown

puts %{</outline>
  </body>
</opml>}