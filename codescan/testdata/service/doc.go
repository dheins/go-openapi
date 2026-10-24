// Package service is a fixture of codescan: an annotated service.
//
//goapi:info title="Pet service" version=1.2.0 description="Pets, as Go code describes them."
//goapi:server https://api.example.com/v1 description=Production
//goapi:server http://localhost:8080/v1
//goapi:securityScheme apiKey type=apiKey in=header name=X-API-Key
//goapi:securityScheme bearer type=http scheme=bearer bearerFormat=JWT
//goapi:securityScheme oauth type=oauth2 flow=clientCredentials tokenUrl=https://auth.example.com/token scopes=read,write
//goapi:security apiKey
//goapi:tag pets description="Everything about pets"
package service
