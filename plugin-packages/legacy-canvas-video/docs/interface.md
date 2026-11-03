# KKAI视频模型协议

## 协议

| Provider | 创建与查询 | 特殊规则 |
| --- | --- | --- |
| `legacy-canvas-video-v1` | `POST/GET /video/generations` | 支持旧 `@参考图1` 标记并转换为 `REFERENCE_n`。 |
| `legacy-canvas-video-v2` | `POST/GET /videos` | 提交 images、videos、audios，并把旧参考素材标记转为 `@Image1`、`@Video1`、`@Audio1`。 |
| `legacy-canvas-video-v3` | 4–29 秒为 `/videos/generations`；30 秒为 `/videos` | 两个分支分别使用其上游字段和查询路径。 |
| `legacy-canvas-video-grok` | `POST /videos`、`GET /videos/{taskId}` | 参考图片使用 multipart 的重复 `input_reference` 文件字段。 |
| `legacy-canvas-video-h3` | `POST/GET /videos` | 根据图片和分辨率自动选择 `text-to-video`、`multi-reference` 或 `cf-multi-reference` 工作流。 |

## 使用说明

这五个协议使用 Bearer API Key。请在渠道中配置自己的 Base URL 和密钥，并为每个模型确认后台能力配置。模型能力限制会阻止超出图片、视频、音频、时长或分辨率范围的请求。

异步结果会从常见的 `task_id`、`status`、`result_url`、`video_url`、`url` 等字段解析；临时成片 URL 由服务器下载并持久化。

## 参数约束

- 模型名完整透传，包括中文计费后缀。V2/V3 的固定分辨率 SKU 会锁定相应档位，V3 的 30 秒按条 480P 模型仍使用 480p。
- V2 满血兜底版固定 15 秒、720p、横屏或竖屏、4000 字提示词；上游请求省略 `resolution` 和 `generate_audio`。其他模型提示词上限为 8000 字。
- Grok 多图生成选择 1080p 时，准入与计费前统一调整为 720p；单图仍允许 1080p。参考图通过重复的 multipart 文件字段上传。
- H3 普通档使用精确像素，切换比例时保持同一尺寸档位；2K/4K 至少需要一张图。参考视频或音频也必须同时携带图片，不会静默丢弃素材。
- V1/V3 的上游状态完成但视频地址尚未出现时继续轮询；V2/Grok/H3 可通过 `/videos/{id}/content` 获取成片。显式失败状态、非成功业务码或非空错误对象会结束任务并显示错误。

具体时长、比例、素材数量和大小由下方合同及前后端同名能力配置约束。个人渠道、系统渠道、画布、创作及 Agent 媒体工具共用后端任务生命周期；重进页面不会重新创建上游任务。

<!-- YINGCE_MANIFEST_CONTRACT_START -->
## Manifest 完整接口定义

以下 JSON 与插件包内实际 `manifest.json` 逐字段一致，覆盖插件身份、权限、配置、鉴权、参数、校验、创建、Agent、查询、取消、结果下载、响应和 Agent 响应映射。`documentation` 字段的值就是当前完整文档；为避免文档在自身内部无限递归，JSON 中仅用等义占位文本表示正文。

```json
{
  "apiVersion": "yingce.plugin/v2",
  "id": "legacy-canvas-video",
  "name": "KKAI视频模型协议",
  "version": "1.0.0",
  "author": "影策",
  "description": "兼容KKAI视频模型 V1、V2、V3、Grok 与 MiniMax H3 视频请求格式。",
  "documentation": "<当前插件的完整 documentation，由 README.md 与 docs/interface.md 拼接而成；为避免 JSON 递归，此处不重复展开正文。>",
  "runtime": {
    "backend": "declarative"
  },
  "permissions": [
    "generation.run",
    "media.read"
  ],
  "configuration": {
    "fields": [
      {
        "name": "apiKey",
        "type": "secret",
        "label": "API Key",
        "required": true
      }
    ]
  },
  "contributes": {
    "providers": [
      {
        "id": "legacy-canvas-video-v1",
        "label": "KKAI视频模型 V1",
        "capabilities": [
          "video"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "requiresPublicMediaUrls": true,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "上游的完整模型 ID。"
          },
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "prompt",
            "description": "视频提示词；V1/V2 会校验并转换旧参考素材编号。"
          },
          {
            "name": "duration",
            "type": "integer",
            "mapping": "duration",
            "description": "输出视频时长，单位秒；具体范围由所选型号能力约束。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "mapping": "aspect_ratio",
            "description": "画面比例；H3 普通档位使用对应比例的精确像素。"
          },
          {
            "name": "images",
            "type": "media[]",
            "mapping": "images",
            "description": "按画布顺序传入的参考图片；Grok 使用重复文件字段。"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/video/generations",
          "contentType": "application/json",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "prompt": {
              "$legacyCanvasV1Prompt": true
            },
            "duration": {
              "$ref": "request.duration"
            },
            "aspect_ratio": {
              "$coalesce": [
                {
                  "$ref": "request.aspectRatio"
                },
                "16:9"
              ]
            },
            "images": {
              "$omitEmpty": {
                "$map": {
                  "from": {
                    "$sortByOrder": {
                      "$ref": "request.images"
                    }
                  },
                  "as": "media",
                  "in": {
                    "$ref": "media.value"
                  }
                }
              }
            }
          }
        },
        "poll": {
          "method": "GET",
          "path": "/video/generations/{{taskId}}"
        },
        "response": {
          "taskId": {
            "$coalesce": [
              {
                "$ref": "response.data.data.task_id"
              },
              {
                "$ref": "response.data.data.taskId"
              },
              {
                "$ref": "response.data.data.id"
              },
              {
                "$ref": "response.data.task_id"
              },
              {
                "$ref": "response.data.taskId"
              },
              {
                "$ref": "response.data.id"
              },
              {
                "$ref": "response.task_id"
              },
              {
                "$ref": "response.taskId"
              },
              {
                "$ref": "response.id"
              },
              {
                "$ref": "response.task.id"
              },
              {
                "$ref": "taskId"
              }
            ]
          },
          "status": {
            "$if": {
              "condition": {
                "$or": [
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.error"
                        }
                      },
                      0
                    ]
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.data.error"
                        }
                      },
                      0
                    ]
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.data.data.error"
                        }
                      },
                      0
                    ]
                  }
                ]
              },
              "then": "failed",
              "else": {
                "$if": {
                  "condition": {
                    "$and": [
                      {
                        "$in": [
                          {
                            "$lower": {
                              "$coalesce": [
                                {
                                  "$ref": "response.data.data.status"
                                },
                                {
                                  "$ref": "response.data.status"
                                },
                                {
                                  "$ref": "response.status"
                                },
                                {
                                  "$ref": "response.data.task_status"
                                },
                                {
                                  "$ref": "response.task_status"
                                },
                                {
                                  "$ref": "response.data.state"
                                },
                                {
                                  "$ref": "response.state"
                                },
                                "pending"
                              ]
                            }
                          },
                          [
                            "succeeded",
                            "success",
                            "completed",
                            "complete",
                            "done",
                            "task_status_succeed"
                          ]
                        ]
                      },
                      {
                        "$not": {
                          "$coalesce": [
                            {
                              "$ref": "response.data.data.result_url"
                            },
                            {
                              "$ref": "response.data.data.video_url"
                            },
                            {
                              "$ref": "response.data.data.download_url"
                            },
                            {
                              "$ref": "response.data.data.file_url"
                            },
                            {
                              "$ref": "response.data.result_url"
                            },
                            {
                              "$ref": "response.data.video_url"
                            },
                            {
                              "$ref": "response.data.download_url"
                            },
                            {
                              "$ref": "response.data.file_url"
                            },
                            {
                              "$ref": "response.result_url"
                            },
                            {
                              "$ref": "response.video_url"
                            },
                            {
                              "$ref": "response.download_url"
                            },
                            {
                              "$ref": "response.file_url"
                            },
                            {
                              "$ref": "response.result.result_url"
                            },
                            {
                              "$ref": "response.result.video_url"
                            },
                            {
                              "$ref": "response.result.download_url"
                            },
                            {
                              "$ref": "response.result.file_url"
                            },
                            {
                              "$ref": "response.data.result.result_url"
                            },
                            {
                              "$ref": "response.data.result.video_url"
                            },
                            {
                              "$ref": "response.data.result.download_url"
                            },
                            {
                              "$ref": "response.data.result.file_url"
                            },
                            {
                              "$ref": "response.output.result_url"
                            },
                            {
                              "$ref": "response.output.video_url"
                            },
                            {
                              "$ref": "response.output.download_url"
                            },
                            {
                              "$ref": "response.output.file_url"
                            },
                            {
                              "$ref": "response.data.output.result_url"
                            },
                            {
                              "$ref": "response.data.output.video_url"
                            },
                            {
                              "$ref": "response.data.output.download_url"
                            },
                            {
                              "$ref": "response.data.output.file_url"
                            },
                            {
                              "$ref": "response.task.result_url"
                            },
                            {
                              "$ref": "response.task.video_url"
                            },
                            {
                              "$ref": "response.task.download_url"
                            },
                            {
                              "$ref": "response.task.file_url"
                            },
                            {
                              "$ref": "response.data.data.content.video_url"
                            },
                            {
                              "$ref": "response.data.data.content.url"
                            },
                            {
                              "$ref": "response.data.data.metadata.direct_url"
                            },
                            {
                              "$ref": "response.data.data.metadata.url"
                            },
                            {
                              "$ref": "response.data.data.video.url"
                            },
                            {
                              "$ref": "response.data.data.output.url"
                            },
                            {
                              "$ref": "response.data.data.url"
                            },
                            {
                              "$ref": "response.data.content.video_url"
                            },
                            {
                              "$ref": "response.data.content.url"
                            },
                            {
                              "$ref": "response.data.metadata.direct_url"
                            },
                            {
                              "$ref": "response.data.metadata.url"
                            },
                            {
                              "$ref": "response.data.video.url"
                            },
                            {
                              "$ref": "response.data.output.url"
                            },
                            {
                              "$ref": "response.data.url"
                            },
                            {
                              "$ref": "response.content.video_url"
                            },
                            {
                              "$ref": "response.content.url"
                            },
                            {
                              "$ref": "response.metadata.direct_url"
                            },
                            {
                              "$ref": "response.metadata.url"
                            },
                            {
                              "$ref": "response.video.url"
                            },
                            {
                              "$ref": "response.output.url"
                            },
                            {
                              "$ref": "response.url"
                            }
                          ]
                        }
                      }
                    ]
                  },
                  "then": "pending",
                  "else": {
                    "$lower": {
                      "$coalesce": [
                        {
                          "$ref": "response.data.data.status"
                        },
                        {
                          "$ref": "response.data.status"
                        },
                        {
                          "$ref": "response.status"
                        },
                        {
                          "$ref": "response.data.task_status"
                        },
                        {
                          "$ref": "response.task_status"
                        },
                        {
                          "$ref": "response.data.state"
                        },
                        {
                          "$ref": "response.state"
                        },
                        "pending"
                      ]
                    }
                  }
                }
              }
            }
          },
          "message": {
            "$coalesce": [
              {
                "$ref": "response.error.message"
              },
              {
                "$ref": "response.data.error.message"
              },
              {
                "$ref": "response.data.data.error.message"
              },
              {
                "$ref": "response.data.data.message"
              },
              {
                "$ref": "response.data.data.msg"
              },
              {
                "$ref": "response.data.message"
              },
              {
                "$ref": "response.data.msg"
              },
              {
                "$ref": "response.message"
              },
              {
                "$ref": "response.msg"
              },
              {
                "$ref": "response.error"
              },
              {
                "$ref": "response.data.error"
              },
              {
                "$ref": "response.data.data.error"
              }
            ]
          },
          "videos": {
            "$coalesce": [
              {
                "$ref": "response.data.data.result_url"
              },
              {
                "$ref": "response.data.data.video_url"
              },
              {
                "$ref": "response.data.data.download_url"
              },
              {
                "$ref": "response.data.data.file_url"
              },
              {
                "$ref": "response.data.result_url"
              },
              {
                "$ref": "response.data.video_url"
              },
              {
                "$ref": "response.data.download_url"
              },
              {
                "$ref": "response.data.file_url"
              },
              {
                "$ref": "response.result_url"
              },
              {
                "$ref": "response.video_url"
              },
              {
                "$ref": "response.download_url"
              },
              {
                "$ref": "response.file_url"
              },
              {
                "$ref": "response.result.result_url"
              },
              {
                "$ref": "response.result.video_url"
              },
              {
                "$ref": "response.result.download_url"
              },
              {
                "$ref": "response.result.file_url"
              },
              {
                "$ref": "response.data.result.result_url"
              },
              {
                "$ref": "response.data.result.video_url"
              },
              {
                "$ref": "response.data.result.download_url"
              },
              {
                "$ref": "response.data.result.file_url"
              },
              {
                "$ref": "response.output.result_url"
              },
              {
                "$ref": "response.output.video_url"
              },
              {
                "$ref": "response.output.download_url"
              },
              {
                "$ref": "response.output.file_url"
              },
              {
                "$ref": "response.data.output.result_url"
              },
              {
                "$ref": "response.data.output.video_url"
              },
              {
                "$ref": "response.data.output.download_url"
              },
              {
                "$ref": "response.data.output.file_url"
              },
              {
                "$ref": "response.task.result_url"
              },
              {
                "$ref": "response.task.video_url"
              },
              {
                "$ref": "response.task.download_url"
              },
              {
                "$ref": "response.task.file_url"
              },
              {
                "$ref": "response.data.data.content.video_url"
              },
              {
                "$ref": "response.data.data.content.url"
              },
              {
                "$ref": "response.data.data.metadata.direct_url"
              },
              {
                "$ref": "response.data.data.metadata.url"
              },
              {
                "$ref": "response.data.data.video.url"
              },
              {
                "$ref": "response.data.data.output.url"
              },
              {
                "$ref": "response.data.data.url"
              },
              {
                "$ref": "response.data.content.video_url"
              },
              {
                "$ref": "response.data.content.url"
              },
              {
                "$ref": "response.data.metadata.direct_url"
              },
              {
                "$ref": "response.data.metadata.url"
              },
              {
                "$ref": "response.data.video.url"
              },
              {
                "$ref": "response.data.output.url"
              },
              {
                "$ref": "response.data.url"
              },
              {
                "$ref": "response.content.video_url"
              },
              {
                "$ref": "response.content.url"
              },
              {
                "$ref": "response.metadata.direct_url"
              },
              {
                "$ref": "response.metadata.url"
              },
              {
                "$ref": "response.video.url"
              },
              {
                "$ref": "response.output.url"
              },
              {
                "$ref": "response.url"
              }
            ]
          },
          "errorPaths": [
            "error.code",
            "error.message",
            "data.error.code",
            "data.error.message",
            "data.data.error.code",
            "data.data.error.message",
            "error_code",
            "data.error_code"
          ],
          "resultEphemeral": true
        }
      },
      {
        "id": "legacy-canvas-video-v2",
        "label": "KKAI视频模型 V2",
        "capabilities": [
          "video"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "requiresPublicMediaUrls": true,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "上游的完整模型 ID。"
          },
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "prompt",
            "description": "视频提示词；V1/V2 会校验并转换旧参考素材编号。"
          },
          {
            "name": "duration",
            "type": "integer",
            "mapping": "duration",
            "description": "输出视频时长，单位秒；具体范围由所选型号能力约束。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "mapping": "aspect_ratio",
            "description": "画面比例；H3 普通档位使用对应比例的精确像素。"
          },
          {
            "name": "resolution",
            "type": "string",
            "mapping": "resolution",
            "description": "输出分辨率；固定档位型号不能切换其他档位。"
          },
          {
            "name": "images",
            "type": "media[]",
            "mapping": "images",
            "description": "按画布顺序传入的参考图片；Grok 使用重复文件字段。"
          },
          {
            "name": "videos",
            "type": "media[]",
            "mapping": "videos",
            "description": "按画布顺序传入的参考视频 URL。"
          },
          {
            "name": "audios",
            "type": "media[]",
            "mapping": "audios",
            "description": "按画布顺序传入的参考音频 URL。"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/videos",
          "contentType": "application/json",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "prompt": {
              "$legacyCanvasV2Prompt": true
            },
            "images": {
              "$map": {
                "from": {
                  "$sortByOrder": {
                    "$ref": "request.images"
                  }
                },
                "as": "media",
                "in": {
                  "$ref": "media.value"
                }
              }
            },
            "videos": {
              "$map": {
                "from": {
                  "$sortByOrder": {
                    "$ref": "request.videos"
                  }
                },
                "as": "media",
                "in": {
                  "$ref": "media.value"
                }
              }
            },
            "audios": {
              "$map": {
                "from": {
                  "$sortByOrder": {
                    "$ref": "request.audios"
                  }
                },
                "as": "media",
                "in": {
                  "$ref": "media.value"
                }
              }
            },
            "aspect_ratio": {
              "$coalesce": [
                {
                  "$ref": "request.aspectRatio"
                },
                "16:9"
              ]
            },
            "duration": {
              "$ref": "request.duration"
            },
            "resolution": {
              "$if": {
                "condition": {
                  "$eq": [
                    {
                      "$lower": {
                        "$trim": {
                          "$ref": "request.model"
                        }
                      }
                    },
                    "video-v2-满血兜底版"
                  ]
                },
                "then": null,
                "else": {
                  "$ref": "request.resolution"
                }
              }
            },
            "generate_audio": {
              "$if": {
                "condition": {
                  "$eq": [
                    {
                      "$lower": {
                        "$trim": {
                          "$ref": "request.model"
                        }
                      }
                    },
                    "video-v2-满血兜底版"
                  ]
                },
                "then": null,
                "else": {
                  "$ref": "request.generateAudio"
                }
              }
            }
          }
        },
        "poll": {
          "method": "GET",
          "path": "/videos/{{taskId}}"
        },
        "result": {
          "method": "GET",
          "path": "/videos/{{taskId}}/content",
          "headers": {
            "Accept": "video/mp4"
          }
        },
        "response": {
          "taskId": {
            "$coalesce": [
              {
                "$ref": "response.data.data.task_id"
              },
              {
                "$ref": "response.data.data.taskId"
              },
              {
                "$ref": "response.data.data.id"
              },
              {
                "$ref": "response.data.task_id"
              },
              {
                "$ref": "response.data.taskId"
              },
              {
                "$ref": "response.data.id"
              },
              {
                "$ref": "response.task_id"
              },
              {
                "$ref": "response.taskId"
              },
              {
                "$ref": "response.id"
              },
              {
                "$ref": "response.task.id"
              },
              {
                "$ref": "taskId"
              }
            ]
          },
          "status": {
            "$if": {
              "condition": {
                "$or": [
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.error"
                        }
                      },
                      0
                    ]
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.data.error"
                        }
                      },
                      0
                    ]
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.data.data.error"
                        }
                      },
                      0
                    ]
                  }
                ]
              },
              "then": "failed",
              "else": {
                "$lower": {
                  "$coalesce": [
                    {
                      "$ref": "response.data.data.status"
                    },
                    {
                      "$ref": "response.data.status"
                    },
                    {
                      "$ref": "response.status"
                    },
                    {
                      "$ref": "response.data.task_status"
                    },
                    {
                      "$ref": "response.task_status"
                    },
                    {
                      "$ref": "response.data.state"
                    },
                    {
                      "$ref": "response.state"
                    },
                    "pending"
                  ]
                }
              }
            }
          },
          "message": {
            "$coalesce": [
              {
                "$ref": "response.error.message"
              },
              {
                "$ref": "response.data.error.message"
              },
              {
                "$ref": "response.data.data.error.message"
              },
              {
                "$ref": "response.data.data.message"
              },
              {
                "$ref": "response.data.data.msg"
              },
              {
                "$ref": "response.data.message"
              },
              {
                "$ref": "response.data.msg"
              },
              {
                "$ref": "response.message"
              },
              {
                "$ref": "response.msg"
              },
              {
                "$ref": "response.error"
              },
              {
                "$ref": "response.data.error"
              },
              {
                "$ref": "response.data.data.error"
              }
            ]
          },
          "videos": {
            "$coalesce": [
              {
                "$ref": "response.data.data.result_url"
              },
              {
                "$ref": "response.data.data.video_url"
              },
              {
                "$ref": "response.data.data.download_url"
              },
              {
                "$ref": "response.data.data.file_url"
              },
              {
                "$ref": "response.data.result_url"
              },
              {
                "$ref": "response.data.video_url"
              },
              {
                "$ref": "response.data.download_url"
              },
              {
                "$ref": "response.data.file_url"
              },
              {
                "$ref": "response.result_url"
              },
              {
                "$ref": "response.video_url"
              },
              {
                "$ref": "response.download_url"
              },
              {
                "$ref": "response.file_url"
              },
              {
                "$ref": "response.result.result_url"
              },
              {
                "$ref": "response.result.video_url"
              },
              {
                "$ref": "response.result.download_url"
              },
              {
                "$ref": "response.result.file_url"
              },
              {
                "$ref": "response.data.result.result_url"
              },
              {
                "$ref": "response.data.result.video_url"
              },
              {
                "$ref": "response.data.result.download_url"
              },
              {
                "$ref": "response.data.result.file_url"
              },
              {
                "$ref": "response.output.result_url"
              },
              {
                "$ref": "response.output.video_url"
              },
              {
                "$ref": "response.output.download_url"
              },
              {
                "$ref": "response.output.file_url"
              },
              {
                "$ref": "response.data.output.result_url"
              },
              {
                "$ref": "response.data.output.video_url"
              },
              {
                "$ref": "response.data.output.download_url"
              },
              {
                "$ref": "response.data.output.file_url"
              },
              {
                "$ref": "response.task.result_url"
              },
              {
                "$ref": "response.task.video_url"
              },
              {
                "$ref": "response.task.download_url"
              },
              {
                "$ref": "response.task.file_url"
              },
              {
                "$ref": "response.data.data.content.video_url"
              },
              {
                "$ref": "response.data.data.content.url"
              },
              {
                "$ref": "response.data.data.metadata.direct_url"
              },
              {
                "$ref": "response.data.data.metadata.url"
              },
              {
                "$ref": "response.data.data.video.url"
              },
              {
                "$ref": "response.data.data.output.url"
              },
              {
                "$ref": "response.data.data.url"
              },
              {
                "$ref": "response.data.content.video_url"
              },
              {
                "$ref": "response.data.content.url"
              },
              {
                "$ref": "response.data.metadata.direct_url"
              },
              {
                "$ref": "response.data.metadata.url"
              },
              {
                "$ref": "response.data.video.url"
              },
              {
                "$ref": "response.data.output.url"
              },
              {
                "$ref": "response.data.url"
              },
              {
                "$ref": "response.content.video_url"
              },
              {
                "$ref": "response.content.url"
              },
              {
                "$ref": "response.metadata.direct_url"
              },
              {
                "$ref": "response.metadata.url"
              },
              {
                "$ref": "response.video.url"
              },
              {
                "$ref": "response.output.url"
              },
              {
                "$ref": "response.url"
              }
            ]
          },
          "errorPaths": [
            "error.code",
            "error.message",
            "data.error.code",
            "data.error.message",
            "data.data.error.code",
            "data.data.error.message",
            "error_code",
            "data.error_code"
          ],
          "resultEphemeral": true
        }
      },
      {
        "id": "legacy-canvas-video-v3",
        "label": "KKAI视频模型 V3",
        "capabilities": [
          "video"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "requiresPublicMediaUrls": true,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "上游的完整模型 ID。"
          },
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "prompt",
            "description": "视频提示词；V1/V2 会校验并转换旧参考素材编号。"
          },
          {
            "name": "duration",
            "type": "integer",
            "mapping": "duration",
            "description": "输出视频时长，单位秒；具体范围由所选型号能力约束。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "mapping": "aspect_ratio",
            "description": "画面比例；H3 普通档位使用对应比例的精确像素。"
          },
          {
            "name": "resolution",
            "type": "string",
            "mapping": "resolution",
            "description": "输出分辨率；固定档位型号不能切换其他档位。"
          },
          {
            "name": "images",
            "type": "media[]",
            "mapping": "images",
            "description": "按画布顺序传入的参考图片；Grok 使用重复文件字段。"
          },
          {
            "name": "videos",
            "type": "media[]",
            "mapping": "videos",
            "description": "按画布顺序传入的参考视频 URL。"
          },
          {
            "name": "audios",
            "type": "media[]",
            "mapping": "audios",
            "description": "按画布顺序传入的参考音频 URL。"
          }
        ],
        "create": {
          "method": "POST",
          "pathTemplate": {
            "$if": {
              "condition": {
                "$lte": [
                  {
                    "$ref": "request.duration"
                  },
                  29
                ]
              },
              "then": "/videos/generations",
              "else": "/videos"
            }
          },
          "contentType": "application/json",
          "body": {
            "$merge": [
              {
                "model": {
                  "$ref": "request.model"
                },
                "prompt": {
                  "$trim": {
                    "$ref": "request.prompt"
                  }
                },
                "duration": {
                  "$ref": "request.duration"
                },
                "resolution": {
                  "$ref": "request.resolution"
                },
                "images": {
                  "$omitEmpty": {
                    "$map": {
                      "from": {
                        "$sortByOrder": {
                          "$ref": "request.images"
                        }
                      },
                      "as": "media",
                      "in": {
                        "$ref": "media.value"
                      }
                    }
                  }
                },
                "videos": {
                  "$omitEmpty": {
                    "$map": {
                      "from": {
                        "$sortByOrder": {
                          "$ref": "request.videos"
                        }
                      },
                      "as": "media",
                      "in": {
                        "$ref": "media.value"
                      }
                    }
                  }
                },
                "audios": {
                  "$omitEmpty": {
                    "$map": {
                      "from": {
                        "$sortByOrder": {
                          "$ref": "request.audios"
                        }
                      },
                      "as": "media",
                      "in": {
                        "$ref": "media.value"
                      }
                    }
                  }
                }
              },
              {
                "$if": {
                  "condition": {
                    "$lte": [
                      {
                        "$ref": "request.duration"
                      },
                      29
                    ]
                  },
                  "then": {
                    "aspect_ratio": {
                      "$coalesce": [
                        {
                          "$ref": "request.aspectRatio"
                        },
                        "16:9"
                      ]
                    },
                    "audio": {
                      "$ref": "request.generateAudio"
                    }
                  },
                  "else": {
                    "ratio": {
                      "$coalesce": [
                        {
                          "$ref": "request.aspectRatio"
                        },
                        "16:9"
                      ]
                    },
                    "generate_audio": {
                      "$ref": "request.generateAudio"
                    }
                  }
                }
              }
            ]
          }
        },
        "poll": {
          "method": "GET",
          "pathTemplate": {
            "$if": {
              "condition": {
                "$lte": [
                  {
                    "$ref": "request.duration"
                  },
                  29
                ]
              },
              "then": "/videos/generations/{{taskId}}",
              "else": "/videos/{{taskId}}"
            }
          }
        },
        "response": {
          "taskId": {
            "$coalesce": [
              {
                "$ref": "response.data.data.task_id"
              },
              {
                "$ref": "response.data.data.taskId"
              },
              {
                "$ref": "response.data.data.id"
              },
              {
                "$ref": "response.data.task_id"
              },
              {
                "$ref": "response.data.taskId"
              },
              {
                "$ref": "response.data.id"
              },
              {
                "$ref": "response.task_id"
              },
              {
                "$ref": "response.taskId"
              },
              {
                "$ref": "response.id"
              },
              {
                "$ref": "response.task.id"
              },
              {
                "$ref": "taskId"
              }
            ]
          },
          "status": {
            "$if": {
              "condition": {
                "$or": [
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.error"
                        }
                      },
                      0
                    ]
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.data.error"
                        }
                      },
                      0
                    ]
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.data.data.error"
                        }
                      },
                      0
                    ]
                  }
                ]
              },
              "then": "failed",
              "else": {
                "$if": {
                  "condition": {
                    "$and": [
                      {
                        "$in": [
                          {
                            "$lower": {
                              "$coalesce": [
                                {
                                  "$ref": "response.data.data.status"
                                },
                                {
                                  "$ref": "response.data.status"
                                },
                                {
                                  "$ref": "response.status"
                                },
                                {
                                  "$ref": "response.data.task_status"
                                },
                                {
                                  "$ref": "response.task_status"
                                },
                                {
                                  "$ref": "response.data.state"
                                },
                                {
                                  "$ref": "response.state"
                                },
                                "pending"
                              ]
                            }
                          },
                          [
                            "succeeded",
                            "success",
                            "completed",
                            "complete",
                            "done",
                            "task_status_succeed"
                          ]
                        ]
                      },
                      {
                        "$not": {
                          "$coalesce": [
                            {
                              "$ref": "response.data.data.result_url"
                            },
                            {
                              "$ref": "response.data.data.video_url"
                            },
                            {
                              "$ref": "response.data.data.download_url"
                            },
                            {
                              "$ref": "response.data.data.file_url"
                            },
                            {
                              "$ref": "response.data.result_url"
                            },
                            {
                              "$ref": "response.data.video_url"
                            },
                            {
                              "$ref": "response.data.download_url"
                            },
                            {
                              "$ref": "response.data.file_url"
                            },
                            {
                              "$ref": "response.result_url"
                            },
                            {
                              "$ref": "response.video_url"
                            },
                            {
                              "$ref": "response.download_url"
                            },
                            {
                              "$ref": "response.file_url"
                            },
                            {
                              "$ref": "response.result.result_url"
                            },
                            {
                              "$ref": "response.result.video_url"
                            },
                            {
                              "$ref": "response.result.download_url"
                            },
                            {
                              "$ref": "response.result.file_url"
                            },
                            {
                              "$ref": "response.data.result.result_url"
                            },
                            {
                              "$ref": "response.data.result.video_url"
                            },
                            {
                              "$ref": "response.data.result.download_url"
                            },
                            {
                              "$ref": "response.data.result.file_url"
                            },
                            {
                              "$ref": "response.output.result_url"
                            },
                            {
                              "$ref": "response.output.video_url"
                            },
                            {
                              "$ref": "response.output.download_url"
                            },
                            {
                              "$ref": "response.output.file_url"
                            },
                            {
                              "$ref": "response.data.output.result_url"
                            },
                            {
                              "$ref": "response.data.output.video_url"
                            },
                            {
                              "$ref": "response.data.output.download_url"
                            },
                            {
                              "$ref": "response.data.output.file_url"
                            },
                            {
                              "$ref": "response.task.result_url"
                            },
                            {
                              "$ref": "response.task.video_url"
                            },
                            {
                              "$ref": "response.task.download_url"
                            },
                            {
                              "$ref": "response.task.file_url"
                            },
                            {
                              "$ref": "response.data.data.content.video_url"
                            },
                            {
                              "$ref": "response.data.data.content.url"
                            },
                            {
                              "$ref": "response.data.data.metadata.direct_url"
                            },
                            {
                              "$ref": "response.data.data.metadata.url"
                            },
                            {
                              "$ref": "response.data.data.video.url"
                            },
                            {
                              "$ref": "response.data.data.output.url"
                            },
                            {
                              "$ref": "response.data.data.url"
                            },
                            {
                              "$ref": "response.data.content.video_url"
                            },
                            {
                              "$ref": "response.data.content.url"
                            },
                            {
                              "$ref": "response.data.metadata.direct_url"
                            },
                            {
                              "$ref": "response.data.metadata.url"
                            },
                            {
                              "$ref": "response.data.video.url"
                            },
                            {
                              "$ref": "response.data.output.url"
                            },
                            {
                              "$ref": "response.data.url"
                            },
                            {
                              "$ref": "response.content.video_url"
                            },
                            {
                              "$ref": "response.content.url"
                            },
                            {
                              "$ref": "response.metadata.direct_url"
                            },
                            {
                              "$ref": "response.metadata.url"
                            },
                            {
                              "$ref": "response.video.url"
                            },
                            {
                              "$ref": "response.output.url"
                            },
                            {
                              "$ref": "response.url"
                            }
                          ]
                        }
                      }
                    ]
                  },
                  "then": "pending",
                  "else": {
                    "$lower": {
                      "$coalesce": [
                        {
                          "$ref": "response.data.data.status"
                        },
                        {
                          "$ref": "response.data.status"
                        },
                        {
                          "$ref": "response.status"
                        },
                        {
                          "$ref": "response.data.task_status"
                        },
                        {
                          "$ref": "response.task_status"
                        },
                        {
                          "$ref": "response.data.state"
                        },
                        {
                          "$ref": "response.state"
                        },
                        "pending"
                      ]
                    }
                  }
                }
              }
            }
          },
          "message": {
            "$coalesce": [
              {
                "$ref": "response.error.message"
              },
              {
                "$ref": "response.data.error.message"
              },
              {
                "$ref": "response.data.data.error.message"
              },
              {
                "$ref": "response.data.data.message"
              },
              {
                "$ref": "response.data.data.msg"
              },
              {
                "$ref": "response.data.message"
              },
              {
                "$ref": "response.data.msg"
              },
              {
                "$ref": "response.message"
              },
              {
                "$ref": "response.msg"
              },
              {
                "$ref": "response.error"
              },
              {
                "$ref": "response.data.error"
              },
              {
                "$ref": "response.data.data.error"
              }
            ]
          },
          "videos": {
            "$coalesce": [
              {
                "$ref": "response.data.data.result_url"
              },
              {
                "$ref": "response.data.data.video_url"
              },
              {
                "$ref": "response.data.data.download_url"
              },
              {
                "$ref": "response.data.data.file_url"
              },
              {
                "$ref": "response.data.result_url"
              },
              {
                "$ref": "response.data.video_url"
              },
              {
                "$ref": "response.data.download_url"
              },
              {
                "$ref": "response.data.file_url"
              },
              {
                "$ref": "response.result_url"
              },
              {
                "$ref": "response.video_url"
              },
              {
                "$ref": "response.download_url"
              },
              {
                "$ref": "response.file_url"
              },
              {
                "$ref": "response.result.result_url"
              },
              {
                "$ref": "response.result.video_url"
              },
              {
                "$ref": "response.result.download_url"
              },
              {
                "$ref": "response.result.file_url"
              },
              {
                "$ref": "response.data.result.result_url"
              },
              {
                "$ref": "response.data.result.video_url"
              },
              {
                "$ref": "response.data.result.download_url"
              },
              {
                "$ref": "response.data.result.file_url"
              },
              {
                "$ref": "response.output.result_url"
              },
              {
                "$ref": "response.output.video_url"
              },
              {
                "$ref": "response.output.download_url"
              },
              {
                "$ref": "response.output.file_url"
              },
              {
                "$ref": "response.data.output.result_url"
              },
              {
                "$ref": "response.data.output.video_url"
              },
              {
                "$ref": "response.data.output.download_url"
              },
              {
                "$ref": "response.data.output.file_url"
              },
              {
                "$ref": "response.task.result_url"
              },
              {
                "$ref": "response.task.video_url"
              },
              {
                "$ref": "response.task.download_url"
              },
              {
                "$ref": "response.task.file_url"
              },
              {
                "$ref": "response.data.data.content.video_url"
              },
              {
                "$ref": "response.data.data.content.url"
              },
              {
                "$ref": "response.data.data.metadata.direct_url"
              },
              {
                "$ref": "response.data.data.metadata.url"
              },
              {
                "$ref": "response.data.data.video.url"
              },
              {
                "$ref": "response.data.data.output.url"
              },
              {
                "$ref": "response.data.data.url"
              },
              {
                "$ref": "response.data.content.video_url"
              },
              {
                "$ref": "response.data.content.url"
              },
              {
                "$ref": "response.data.metadata.direct_url"
              },
              {
                "$ref": "response.data.metadata.url"
              },
              {
                "$ref": "response.data.video.url"
              },
              {
                "$ref": "response.data.output.url"
              },
              {
                "$ref": "response.data.url"
              },
              {
                "$ref": "response.content.video_url"
              },
              {
                "$ref": "response.content.url"
              },
              {
                "$ref": "response.metadata.direct_url"
              },
              {
                "$ref": "response.metadata.url"
              },
              {
                "$ref": "response.video.url"
              },
              {
                "$ref": "response.output.url"
              },
              {
                "$ref": "response.url"
              }
            ]
          },
          "errorPaths": [
            "error.code",
            "error.message",
            "data.error.code",
            "data.error.message",
            "data.data.error.code",
            "data.data.error.message",
            "error_code",
            "data.error_code"
          ],
          "resultEphemeral": true
        }
      },
      {
        "id": "legacy-canvas-video-grok",
        "label": "KKAI视频模型 Grok Imagine Video",
        "capabilities": [
          "video"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "requiresPublicMediaUrls": false,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "上游的完整模型 ID。"
          },
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "prompt",
            "description": "视频提示词；V1/V2 会校验并转换旧参考素材编号。"
          },
          {
            "name": "duration",
            "type": "integer",
            "mapping": "seconds",
            "description": "输出视频时长，单位秒；具体范围由所选型号能力约束。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "mapping": "aspect_ratio",
            "description": "画面比例；H3 普通档位使用对应比例的精确像素。"
          },
          {
            "name": "resolution",
            "type": "string",
            "mapping": "resolution",
            "description": "输出分辨率；固定档位型号不能切换其他档位。"
          },
          {
            "name": "images",
            "type": "media[]",
            "mapping": "input_reference",
            "description": "按画布顺序传入的参考图片；Grok 使用重复文件字段。"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/videos",
          "contentType": "multipart/form-data",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "prompt": {
              "$ref": "request.prompt"
            },
            "seconds": {
              "$ref": "request.duration"
            },
            "aspect_ratio": {
              "$coalesce": [
                {
                  "$ref": "request.aspectRatio"
                },
                "16:9"
              ]
            },
            "resolution": {
              "$coalesce": [
                {
                  "$ref": "request.resolution"
                },
                "720p"
              ]
            }
          },
          "files": [
            {
              "name": "input_reference",
              "source": {
                "$sortByOrder": {
                  "$ref": "request.images"
                }
              }
            }
          ]
        },
        "poll": {
          "method": "GET",
          "path": "/videos/{{taskId}}"
        },
        "result": {
          "method": "GET",
          "path": "/videos/{{taskId}}/content",
          "headers": {
            "Accept": "video/mp4"
          }
        },
        "response": {
          "taskId": {
            "$coalesce": [
              {
                "$ref": "response.data.data.task_id"
              },
              {
                "$ref": "response.data.data.taskId"
              },
              {
                "$ref": "response.data.data.id"
              },
              {
                "$ref": "response.data.task_id"
              },
              {
                "$ref": "response.data.taskId"
              },
              {
                "$ref": "response.data.id"
              },
              {
                "$ref": "response.task_id"
              },
              {
                "$ref": "response.taskId"
              },
              {
                "$ref": "response.id"
              },
              {
                "$ref": "response.task.id"
              },
              {
                "$ref": "taskId"
              }
            ]
          },
          "status": {
            "$if": {
              "condition": {
                "$or": [
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.error"
                        }
                      },
                      0
                    ]
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.data.error"
                        }
                      },
                      0
                    ]
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.data.data.error"
                        }
                      },
                      0
                    ]
                  }
                ]
              },
              "then": "failed",
              "else": {
                "$lower": {
                  "$coalesce": [
                    {
                      "$ref": "response.data.data.status"
                    },
                    {
                      "$ref": "response.data.status"
                    },
                    {
                      "$ref": "response.status"
                    },
                    {
                      "$ref": "response.data.task_status"
                    },
                    {
                      "$ref": "response.task_status"
                    },
                    {
                      "$ref": "response.data.state"
                    },
                    {
                      "$ref": "response.state"
                    },
                    "pending"
                  ]
                }
              }
            }
          },
          "message": {
            "$coalesce": [
              {
                "$ref": "response.error.message"
              },
              {
                "$ref": "response.data.error.message"
              },
              {
                "$ref": "response.data.data.error.message"
              },
              {
                "$ref": "response.data.data.message"
              },
              {
                "$ref": "response.data.data.msg"
              },
              {
                "$ref": "response.data.message"
              },
              {
                "$ref": "response.data.msg"
              },
              {
                "$ref": "response.message"
              },
              {
                "$ref": "response.msg"
              },
              {
                "$ref": "response.error"
              },
              {
                "$ref": "response.data.error"
              },
              {
                "$ref": "response.data.data.error"
              }
            ]
          },
          "videos": {
            "$coalesce": [
              {
                "$ref": "response.data.data.result_url"
              },
              {
                "$ref": "response.data.data.video_url"
              },
              {
                "$ref": "response.data.data.download_url"
              },
              {
                "$ref": "response.data.data.file_url"
              },
              {
                "$ref": "response.data.result_url"
              },
              {
                "$ref": "response.data.video_url"
              },
              {
                "$ref": "response.data.download_url"
              },
              {
                "$ref": "response.data.file_url"
              },
              {
                "$ref": "response.result_url"
              },
              {
                "$ref": "response.video_url"
              },
              {
                "$ref": "response.download_url"
              },
              {
                "$ref": "response.file_url"
              },
              {
                "$ref": "response.result.result_url"
              },
              {
                "$ref": "response.result.video_url"
              },
              {
                "$ref": "response.result.download_url"
              },
              {
                "$ref": "response.result.file_url"
              },
              {
                "$ref": "response.data.result.result_url"
              },
              {
                "$ref": "response.data.result.video_url"
              },
              {
                "$ref": "response.data.result.download_url"
              },
              {
                "$ref": "response.data.result.file_url"
              },
              {
                "$ref": "response.output.result_url"
              },
              {
                "$ref": "response.output.video_url"
              },
              {
                "$ref": "response.output.download_url"
              },
              {
                "$ref": "response.output.file_url"
              },
              {
                "$ref": "response.data.output.result_url"
              },
              {
                "$ref": "response.data.output.video_url"
              },
              {
                "$ref": "response.data.output.download_url"
              },
              {
                "$ref": "response.data.output.file_url"
              },
              {
                "$ref": "response.task.result_url"
              },
              {
                "$ref": "response.task.video_url"
              },
              {
                "$ref": "response.task.download_url"
              },
              {
                "$ref": "response.task.file_url"
              },
              {
                "$ref": "response.data.data.content.video_url"
              },
              {
                "$ref": "response.data.data.content.url"
              },
              {
                "$ref": "response.data.data.metadata.direct_url"
              },
              {
                "$ref": "response.data.data.metadata.url"
              },
              {
                "$ref": "response.data.data.video.url"
              },
              {
                "$ref": "response.data.data.output.url"
              },
              {
                "$ref": "response.data.data.url"
              },
              {
                "$ref": "response.data.content.video_url"
              },
              {
                "$ref": "response.data.content.url"
              },
              {
                "$ref": "response.data.metadata.direct_url"
              },
              {
                "$ref": "response.data.metadata.url"
              },
              {
                "$ref": "response.data.video.url"
              },
              {
                "$ref": "response.data.output.url"
              },
              {
                "$ref": "response.data.url"
              },
              {
                "$ref": "response.content.video_url"
              },
              {
                "$ref": "response.content.url"
              },
              {
                "$ref": "response.metadata.direct_url"
              },
              {
                "$ref": "response.metadata.url"
              },
              {
                "$ref": "response.video.url"
              },
              {
                "$ref": "response.output.url"
              },
              {
                "$ref": "response.url"
              }
            ]
          },
          "errorPaths": [
            "error.code",
            "error.message",
            "data.error.code",
            "data.error.message",
            "data.data.error.code",
            "data.data.error.message",
            "error_code",
            "data.error_code"
          ],
          "resultEphemeral": true
        }
      },
      {
        "id": "legacy-canvas-video-h3",
        "label": "KKAI视频模型 MiniMax H3",
        "capabilities": [
          "video"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "requiresPublicMediaUrls": true,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "上游的完整模型 ID。"
          },
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "prompt",
            "description": "视频提示词；V1/V2 会校验并转换旧参考素材编号。"
          },
          {
            "name": "duration",
            "type": "integer",
            "mapping": "seconds",
            "description": "输出视频时长，单位秒；具体范围由所选型号能力约束。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "mapping": "aspect_ratio",
            "description": "画面比例；H3 普通档位使用对应比例的精确像素。"
          },
          {
            "name": "resolution",
            "type": "string",
            "mapping": "size",
            "description": "输出分辨率；固定档位型号不能切换其他档位。"
          },
          {
            "name": "images",
            "type": "media[]",
            "mapping": "images",
            "description": "按画布顺序传入的参考图片；Grok 使用重复文件字段。"
          },
          {
            "name": "videos",
            "type": "media[]",
            "mapping": "reference_videos",
            "description": "按画布顺序传入的参考视频 URL。"
          },
          {
            "name": "audios",
            "type": "media[]",
            "mapping": "reference_audios",
            "description": "按画布顺序传入的参考音频 URL。"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/videos",
          "contentType": "application/json",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "prompt": {
              "$trim": {
                "$ref": "request.prompt"
              }
            },
            "workflow_id": {
              "$if": {
                "condition": {
                  "$eq": [
                    {
                      "$len": {
                        "$ref": "request.images"
                      }
                    },
                    0
                  ]
                },
                "then": "text-to-video",
                "else": {
                  "$if": {
                    "condition": {
                      "$in": [
                        {
                          "$upper": {
                            "$ref": "request.resolution"
                          }
                        },
                        [
                          "2K",
                          "4K"
                        ]
                      ]
                    },
                    "then": "cf-multi-reference",
                    "else": "multi-reference"
                  }
                }
              }
            },
            "seconds": {
              "$ref": "request.duration"
            },
            "size": {
              "$ref": "request.resolution"
            },
            "aspect_ratio": {
              "$if": {
                "condition": {
                  "$in": [
                    {
                      "$upper": {
                        "$ref": "request.resolution"
                      }
                    },
                    [
                      "2K",
                      "4K"
                    ]
                  ]
                },
                "then": {
                  "$coalesce": [
                    {
                      "$ref": "request.aspectRatio"
                    },
                    "16:9"
                  ]
                },
                "else": null
              }
            },
            "images": {
              "$omitEmpty": {
                "$map": {
                  "from": {
                    "$sortByOrder": {
                      "$ref": "request.images"
                    }
                  },
                  "as": "media",
                  "in": {
                    "$ref": "media.value"
                  }
                }
              }
            },
            "reference_videos": {
              "$omitEmpty": {
                "$map": {
                  "from": {
                    "$sortByOrder": {
                      "$ref": "request.videos"
                    }
                  },
                  "as": "media",
                  "in": {
                    "$ref": "media.value"
                  }
                }
              }
            },
            "reference_audios": {
              "$omitEmpty": {
                "$map": {
                  "from": {
                    "$sortByOrder": {
                      "$ref": "request.audios"
                    }
                  },
                  "as": "media",
                  "in": {
                    "$ref": "media.value"
                  }
                }
              }
            }
          }
        },
        "poll": {
          "method": "GET",
          "path": "/videos/{{taskId}}"
        },
        "result": {
          "method": "GET",
          "path": "/videos/{{taskId}}/content",
          "headers": {
            "Accept": "video/mp4"
          }
        },
        "response": {
          "taskId": {
            "$coalesce": [
              {
                "$ref": "response.data.data.task_id"
              },
              {
                "$ref": "response.data.data.taskId"
              },
              {
                "$ref": "response.data.data.id"
              },
              {
                "$ref": "response.data.task_id"
              },
              {
                "$ref": "response.data.taskId"
              },
              {
                "$ref": "response.data.id"
              },
              {
                "$ref": "response.task_id"
              },
              {
                "$ref": "response.taskId"
              },
              {
                "$ref": "response.id"
              },
              {
                "$ref": "response.task.id"
              },
              {
                "$ref": "taskId"
              }
            ]
          },
          "status": {
            "$if": {
              "condition": {
                "$or": [
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$not": {
                      "$or": [
                        {
                          "$in": [
                            {
                              "$lower": {
                                "$toString": {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                }
                              }
                            },
                            [
                              "0",
                              "ok",
                              "success",
                              "succeeded"
                            ]
                          ]
                        },
                        {
                          "$and": [
                            {
                              "$gte": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                200
                              ]
                            },
                            {
                              "$lt": [
                                {
                                  "$coalesce": [
                                    {
                                      "$ref": "response.data.data.code"
                                    },
                                    "0"
                                  ]
                                },
                                300
                              ]
                            }
                          ]
                        }
                      ]
                    }
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.error"
                        }
                      },
                      0
                    ]
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.data.error"
                        }
                      },
                      0
                    ]
                  },
                  {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.data.data.error"
                        }
                      },
                      0
                    ]
                  }
                ]
              },
              "then": "failed",
              "else": {
                "$lower": {
                  "$coalesce": [
                    {
                      "$ref": "response.data.data.status"
                    },
                    {
                      "$ref": "response.data.status"
                    },
                    {
                      "$ref": "response.status"
                    },
                    {
                      "$ref": "response.data.task_status"
                    },
                    {
                      "$ref": "response.task_status"
                    },
                    {
                      "$ref": "response.data.state"
                    },
                    {
                      "$ref": "response.state"
                    },
                    "pending"
                  ]
                }
              }
            }
          },
          "message": {
            "$coalesce": [
              {
                "$ref": "response.error.message"
              },
              {
                "$ref": "response.data.error.message"
              },
              {
                "$ref": "response.data.data.error.message"
              },
              {
                "$ref": "response.data.data.message"
              },
              {
                "$ref": "response.data.data.msg"
              },
              {
                "$ref": "response.data.message"
              },
              {
                "$ref": "response.data.msg"
              },
              {
                "$ref": "response.message"
              },
              {
                "$ref": "response.msg"
              },
              {
                "$ref": "response.error"
              },
              {
                "$ref": "response.data.error"
              },
              {
                "$ref": "response.data.data.error"
              }
            ]
          },
          "videos": {
            "$coalesce": [
              {
                "$ref": "response.data.data.result_url"
              },
              {
                "$ref": "response.data.data.video_url"
              },
              {
                "$ref": "response.data.data.download_url"
              },
              {
                "$ref": "response.data.data.file_url"
              },
              {
                "$ref": "response.data.result_url"
              },
              {
                "$ref": "response.data.video_url"
              },
              {
                "$ref": "response.data.download_url"
              },
              {
                "$ref": "response.data.file_url"
              },
              {
                "$ref": "response.result_url"
              },
              {
                "$ref": "response.video_url"
              },
              {
                "$ref": "response.download_url"
              },
              {
                "$ref": "response.file_url"
              },
              {
                "$ref": "response.result.result_url"
              },
              {
                "$ref": "response.result.video_url"
              },
              {
                "$ref": "response.result.download_url"
              },
              {
                "$ref": "response.result.file_url"
              },
              {
                "$ref": "response.data.result.result_url"
              },
              {
                "$ref": "response.data.result.video_url"
              },
              {
                "$ref": "response.data.result.download_url"
              },
              {
                "$ref": "response.data.result.file_url"
              },
              {
                "$ref": "response.output.result_url"
              },
              {
                "$ref": "response.output.video_url"
              },
              {
                "$ref": "response.output.download_url"
              },
              {
                "$ref": "response.output.file_url"
              },
              {
                "$ref": "response.data.output.result_url"
              },
              {
                "$ref": "response.data.output.video_url"
              },
              {
                "$ref": "response.data.output.download_url"
              },
              {
                "$ref": "response.data.output.file_url"
              },
              {
                "$ref": "response.task.result_url"
              },
              {
                "$ref": "response.task.video_url"
              },
              {
                "$ref": "response.task.download_url"
              },
              {
                "$ref": "response.task.file_url"
              },
              {
                "$ref": "response.data.data.content.video_url"
              },
              {
                "$ref": "response.data.data.content.url"
              },
              {
                "$ref": "response.data.data.metadata.direct_url"
              },
              {
                "$ref": "response.data.data.metadata.url"
              },
              {
                "$ref": "response.data.data.video.url"
              },
              {
                "$ref": "response.data.data.output.url"
              },
              {
                "$ref": "response.data.data.url"
              },
              {
                "$ref": "response.data.content.video_url"
              },
              {
                "$ref": "response.data.content.url"
              },
              {
                "$ref": "response.data.metadata.direct_url"
              },
              {
                "$ref": "response.data.metadata.url"
              },
              {
                "$ref": "response.data.video.url"
              },
              {
                "$ref": "response.data.output.url"
              },
              {
                "$ref": "response.data.url"
              },
              {
                "$ref": "response.content.video_url"
              },
              {
                "$ref": "response.content.url"
              },
              {
                "$ref": "response.metadata.direct_url"
              },
              {
                "$ref": "response.metadata.url"
              },
              {
                "$ref": "response.video.url"
              },
              {
                "$ref": "response.output.url"
              },
              {
                "$ref": "response.url"
              }
            ]
          },
          "errorPaths": [
            "error.code",
            "error.message",
            "data.error.code",
            "data.error.message",
            "data.data.error.code",
            "data.data.error.message",
            "error_code",
            "data.error_code"
          ],
          "resultEphemeral": true
        },
        "validations": [
          {
            "assert": {
              "$not": {
                "$and": [
                  {
                    "$in": [
                      {
                        "$upper": {
                          "$ref": "request.resolution"
                        }
                      },
                      [
                        "2K",
                        "4K"
                      ]
                    ]
                  },
                  {
                    "$eq": [
                      {
                        "$len": {
                          "$ref": "request.images"
                        }
                      },
                      0
                    ]
                  }
                ]
              }
            },
            "message": "MiniMax H3 的 2K/4K 生成至少需要 1 张参考图。"
          },
          {
            "assert": {
              "$not": {
                "$and": [
                  {
                    "$eq": [
                      {
                        "$len": {
                          "$ref": "request.images"
                        }
                      },
                      0
                    ]
                  },
                  {
                    "$or": [
                      {
                        "$gt": [
                          {
                            "$len": {
                              "$ref": "request.videos"
                            }
                          },
                          0
                        ]
                      },
                      {
                        "$gt": [
                          {
                            "$len": {
                              "$ref": "request.audios"
                            }
                          },
                          0
                        ]
                      }
                    ]
                  }
                ]
              }
            },
            "message": "MiniMax H3 的参考视频或参考音频必须同时携带至少 1 张参考图。"
          }
        ]
      }
    ]
  }
}
```
<!-- YINGCE_MANIFEST_CONTRACT_END -->
