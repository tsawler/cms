<?php

namespace App\Models;

use App\Services\BaseModel;

class SettingGeneral extends BaseModel
{
    public $columns = [
        [
            'name' => 'app_title',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => 'required',
            'help' => 'Name of this website.',
            'form_type' => '',
        ],
        [
            'name' => 'default_meta_title',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'Title of pages is default_meta_title | page title.',
            'form_type' => '',
        ],
        [
            'name' => 'default_meta_description',
            'type' => 'text',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'Default Description that used in search engines.',
            'form_type' => 'textarea',
        ],
        [
            'name' => 'logo',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => 'required',
            'help' => 'Maximum 100px',
            'form_type' => 'image',
        ],
        [
            'name' => 'favicon',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => 'required',
            'help' => 'Maximum 50px',
            'form_type' => 'image',
        ],
        [
            'name' => 'default_meta_image',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'It can be just like logo image.',
            'form_type' => 'image',
        ],
        [
            'name' => 'default_user_image',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'Image that used for users that have no profile image.',
            'form_type' => 'image',
        ],
        [
            'name' => 'default_product_image',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'Image that used for products.',
            'form_type' => 'image',
        ],
        [
            'name' => 'google_index',
            'type' => 'boolean',
            'database' => 'default',
            'rule' => 'boolean',
            'help' => 'Warning! if it is unchecked means google will ignore this site.',
            'form_type' => 'checkbox',
        ],
        [
            'name' => 'pagination_number',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => 'numeric',
            'help' => 'Its tables pagination number in blog list page',
            'form_type' => '',
        ],
        [
            'name' => 'android_application_url',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'Url of Google play for android application.',
            'form_type' => '',
        ],
        [
            'name' => 'ios_application_url',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'Url of Apple store for ios application.',
            'form_type' => '',
        ],
        [
            'name' => 'introduce_video_url',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'The main video that will show in home page.',
            'form_type' => '',
        ],
        [
            'name' => 'introduce_video_cover_photo',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'Cover photo for introduce video.',
            'form_type' => 'image',
        ],
        [
            'name' => 'google_map_code',
            'type' => 'text',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'Google map code for using map in site.',
            'form_type' => '',
        ],
        [
            'name' => 'site_verification_google_code',
            'type' => 'text',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'google webmaster meta code.',
            'form_type' => '',
        ],
        [
            'name' => 'google_analytics_id',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'ID of Google Analytics.',
            'form_type' => '',
        ],
        [
            'name' => 'hotjar_id',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'ID of Hotjar.',
            'form_type' => '',
        ],
        [
            'name' => 'crisp_id',
            'type' => 'string',
            'database' => 'nullable',
            'rule' => '',
            'help' => 'ID of crisp chat.',
            'form_type' => '',
        ],
    ];
}
